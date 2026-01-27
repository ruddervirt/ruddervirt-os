#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
import sys
import os
import subprocess
import argparse
import urllib.request
import urllib.error
import json
import tempfile
import glob
from passlib.hash import sha512_crypt
from jinja2 import Environment, FileSystemLoader
from pathlib import Path


def print_file_to_console(label: str, path: str | Path) -> None:
    p = path if isinstance(path, Path) else Path(path)
    content = p.read_text(encoding="utf-8")
    print(f"\n===== BEGIN {label}: {p} =====\n")
    print(content, end="" if content.endswith("\n") else "\n")
    print(f"\n===== END {label}: {p} =====\n")

def fetch_github_ssh_keys(username):
    url = f"https://github.com/{username}.keys"
    try:
        with urllib.request.urlopen(url) as response:
            if response.getcode() == 200:
                keys = response.read().decode('utf-8').strip().split('\n')
                keys = [key.strip() for key in keys if key.strip()]
                if not keys:
                    print(f"Warning: No SSH keys found for GitHub user '{username}'")
                    return []
                print(f"Found {len(keys)} SSH key(s) for GitHub user '{username}'")
                return keys
            else:
                print(f"Error: Unable to fetch SSH keys for user '{username}' (HTTP {response.getcode()})")
                return []
    except urllib.error.URLError as e:
        print(f"Error fetching SSH keys for user '{username}': {e}")
        return []



def hash_password_sha512(plaintext_password: str) -> str:
    if not plaintext_password:
        raise ValueError("Password must not be empty")
    return sha512_crypt.hash(plaintext_password)


def template_butane(
    butane_file: str,
    password_hash: str | None,
    ssh_keys: list[str] | None = None,
    disable_autoupdate: bool = False,
    disk_path: str | None = None,
) -> str:
    template_path = Path(butane_file).resolve()
    template_dir = template_path.parent

    env = Environment(
        loader=FileSystemLoader(str(template_dir)),
        autoescape=False,
        keep_trailing_newline=True,
    )

    def slurp(rel_path: str) -> str:
        return (template_dir / rel_path).read_text(encoding="utf-8")

    def manifest_files(rel_dir: str = "manifests") -> list[str]:
        base = (template_dir / rel_dir).resolve()
        if not base.exists():
            return []
        if not base.is_dir():
            raise ValueError(f"{rel_dir} must be a directory")

        names: list[str] = []
        for entry in base.iterdir():
            if not entry.is_file():
                continue
            if entry.name.startswith("."):
                continue
            if entry.suffix.lower() not in (".yaml", ".yml"):
                continue
            names.append(entry.name)

        return sorted(names)

    env.globals["slurp"] = slurp
    env.globals["manifest_files"] = manifest_files

    template = env.get_template(template_path.name)
    rendered_content = template.render(
        password_hash=password_hash,
        ssh_keys=ssh_keys or [],
        disable_autoupdate=disable_autoupdate,
        disk_path=disk_path,
    )

    temp_fd, temp_path = tempfile.mkstemp(suffix='.bu', prefix='server_rendered_')
    try:
        with os.fdopen(temp_fd, 'w') as f:
            f.write(rendered_content)
        print(f"Created temporary Butane file: {temp_path}")
        return temp_path
    except Exception:
        os.close(temp_fd)
        if os.path.exists(temp_path):
            os.unlink(temp_path)
        raise


def run_command(cmd, check=True):
    print(f"Running: {' '.join(cmd) if isinstance(cmd, list) else cmd}")
    
    result = subprocess.run(
        cmd,
        shell=isinstance(cmd, str),
        text=True
    )
    
    if check and result.returncode != 0:
        print(f"Error running command: {cmd}")
        print(f"Exit code: {result.returncode}")
        sys.exit(1)
    
    return result


def fetch_json(url: str, timeout: int = 20) -> dict:
    req = urllib.request.Request(url, headers={"User-Agent": "ruddervirtvirt-os/create-iso"})
    with urllib.request.urlopen(req, timeout=timeout) as response:
        if response.getcode() != 200:
            raise RuntimeError(f"Failed to fetch JSON: {url} (HTTP {response.getcode()})")
        body = response.read().decode("utf-8")
    return json.loads(body)


def resolve_latest_fcos_release(stream: str, arch: str) -> str:
    fcos_stream_metadata_url_template = "https://builds.coreos.fedoraproject.org/streams/{stream}.json"

    url = fcos_stream_metadata_url_template.format(stream=stream)
    data = fetch_json(url=url)

    try:
        release = data["architectures"][arch]["artifacts"]["metal"]["release"]
    except Exception:
        release = None

    if not release:
        raise RuntimeError(
            f"Unable to determine latest Fedora CoreOS release from {url} for arch={arch}. "
            "The stream metadata format may have changed."
        )

    return release


def build_live_rootfs_url(stream: str, arch: str, release: str) -> str:
    fcos_prod_stream_build_url_template = (
        "https://builds.coreos.fedoraproject.org/prod/streams/{stream}/builds/{release}/{arch}/{filename}"
    )

    filename = f"fedora-coreos-{release}-live-rootfs.{arch}.img"
    return fcos_prod_stream_build_url_template.format(
        stream=stream,
        release=release,
        arch=arch,
        filename=filename,
    )


def main():
    parser = argparse.ArgumentParser(description="Create CoreOS installation ISO with embedded ignition config")
    parser.add_argument("install_disk", help="Target installation disk device")
    parser.add_argument(
        "password",
        nargs="?",
        help="Plaintext password to set for the installed OS (optional if --github-ssh-user is provided).",
    )
    parser.add_argument(
        "--github-ssh-user",
        action="append",
        default=[],
        metavar="USERNAME",
        help="GitHub username to fetch SSH public keys from (can be specified multiple times).",
    )
    parser.add_argument(
        "--disable-autoupdate",
        action="store_true",
        help="Disable Zincati auto-updates (takes priority over any periodic schedule).",
    )
    parser.add_argument(
        "--show-butane",
        action="store_true",
        help="Print the rendered Butane config to stdout.",
    )
    parser.add_argument(
        "--show-ignition",
        action="store_true",
        help="Print the generated Ignition config to stdout.",
    )
    
    args = parser.parse_args()
    
    input_butane = "server.bu.j2"
    install_disk = args.install_disk
    password = args.password
    stream = "stable"
    arch = "x86_64"
    output_ignition = Path(input_butane).with_suffix('.ign')
    output_iso = "/output/ruddervirtvirt-install.iso"
    fedora_iso = "fedora-coreos.iso"    
    temp_butane_file = None
    
    try:
        ssh_keys: list[str] = []
        for username in args.github_ssh_user:
            ssh_keys.extend(fetch_github_ssh_keys(username=username))

        ssh_keys = set(ssh_keys)

        if password:
            password_hash: str | None = hash_password_sha512(plaintext_password=password)
        elif ssh_keys:
            password_hash = None
        else:
            raise ValueError("Password is required unless --github-ssh-user provides at least one SSH key")

        temp_butane_file = template_butane(
            butane_file=input_butane,
            password_hash=password_hash,
            ssh_keys=sorted(ssh_keys),
            disable_autoupdate=args.disable_autoupdate,
            disk_path=install_disk,
        )
        input_butane = temp_butane_file

        if args.show_butane:
            print_file_to_console(label="BUTANE", path=input_butane)

        print("Generating ignition")
        run_command(
            cmd=["butane", "--pretty", "--strict", input_butane, "--output", str(output_ignition)]
        )

        if args.show_ignition:
            print_file_to_console(label="IGNITION", path=output_ignition)
        
        run_command(cmd=["ignition-validate", str(output_ignition)])
        
        if not os.path.exists(fedora_iso):
            print("Downloading Fedora CoreOS")
            run_command(
                cmd=[
                    "coreos-installer",
                    "download",
                    "-f",
                    "iso",
                    "-s",
                    stream,
                    "--decompress",
                    "--architecture",
                    arch,
                ]
            )
            downloaded_isos = glob.glob("*.iso")
            if downloaded_isos:
                downloaded_iso = downloaded_isos[0]
                os.rename(downloaded_iso, fedora_iso)
                print(f"Renamed {downloaded_iso} to {fedora_iso}")
            else:
                print("Failed to find downloaded Fedora CoreOS ISO file")
                sys.exit(1)
        
        if os.path.exists(output_iso):
            os.remove(output_iso)
        
        release = resolve_latest_fcos_release(stream=stream, arch=arch)
        rootfs_url = build_live_rootfs_url(stream=stream, arch=arch, release=release)

        print(f"Using live rootfs URL: {rootfs_url}")

        minimal_iso = "minimal.iso"
        if os.path.exists(minimal_iso):
            os.remove(minimal_iso)

        run_command(
            cmd=[
                "coreos-installer",
                "iso",
                "extract",
                "minimal-iso",
                "--rootfs-url",
                rootfs_url,
                fedora_iso,
                minimal_iso,
            ]
        )

        run_command(
            cmd=[
                "coreos-installer",
                "iso",
                "customize",
                "-f",
                "--dest-device",
                install_disk,
                "--dest-ignition",
                str(output_ignition),
                "-o",
                output_iso,
                minimal_iso,
            ]
        )

        if os.path.exists(minimal_iso):
            os.remove(minimal_iso)
        
        if os.path.exists(fedora_iso):
            os.remove(fedora_iso)
        
        print(f"Created {output_iso}")
        
    except KeyboardInterrupt:
        print("\nOperation cancelled by user")
        sys.exit(1)
    except Exception as e:
        print(f"Error: {e}")
        sys.exit(1)
    finally:
        if temp_butane_file and os.path.exists(temp_butane_file):
            os.unlink(temp_butane_file)
            print(f"Cleaned up temporary file: {temp_butane_file}")


if __name__ == "__main__":
    main()