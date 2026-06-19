#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
import sys
import os
import subprocess
import argparse
import urllib.request
import json
import glob
from pathlib import Path


def print_file_to_console(label: str, path: str | Path) -> None:
    p = path if isinstance(path, Path) else Path(path)
    content = p.read_text(encoding="utf-8")
    print(f"\n===== BEGIN {label}: {p} =====\n")
    print(content, end="" if content.endswith("\n") else "\n")
    print(f"\n===== END {label}: {p} =====\n")

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
    req = urllib.request.Request(url, headers={"User-Agent": "ruddervirt-os/create-iso"})
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


def rebrand_iso_boot_menu(iso_path: str, old: bytes = b"Fedora CoreOS", new: bytes = b"RudderVirt OS") -> None:
    """Rebrand the live ISO's boot-menu title in place.

    `old` and `new` are the same length, so the bytes are replaced directly in
    the ISO without repacking. This preserves the ISO9660 layout and, crucially,
    coreos-installer's embed area — repacking the ISO (e.g. with xorriso) drops
    that area and breaks `coreos-installer iso customize`. Only uncompressed
    plaintext regions (grub.cfg / isolinux.cfg) hold the literal string, so
    compressed payloads in the ISO are left untouched.
    """
    if len(old) != len(new):
        raise ValueError("rebrand strings must be the same length to preserve ISO layout")
    data = Path(iso_path).read_bytes()
    count = data.count(old)
    if count:
        Path(iso_path).write_bytes(data.replace(old, new))
    print(f"Boot-menu rebrand: replaced {count} occurrence(s) of {old.decode()!r} with {new.decode()!r}")


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
    parser.add_argument(
        "--version",
        default="dev",
        metavar="VERSION",
        help="Version string used in the output ISO filename (default: dev).",
    )
    parser.add_argument(
        "--show-ignition",
        action="store_true",
        help="Print the generated Ignition config to stdout.",
    )

    args = parser.parse_args()

    input_butane = "server.bu"
    stream = "stable"
    arch = "x86_64"
    output_ignition = Path(input_butane).with_suffix('.ign')
    output_iso = f"/output/ruddervirt-install-{args.version}.iso"
    fedora_iso = "fedora-coreos.iso"

    try:
        print("Generating ignition")
        run_command(
            cmd=["butane", "--files-dir", ".", "--pretty", "--strict", input_butane, "--output", str(output_ignition)]
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

        print("Rebranding live ISO boot menu")
        try:
            rebrand_iso_boot_menu(minimal_iso)
        except Exception as e:
            print(f"Warning: boot-menu rebrand failed ({e}); continuing with unbranded menu")

        run_command(
            cmd=[
                "coreos-installer",
                "iso",
                "customize",
                "-f",
                "--pre-install",
                "scripts/install-menu.sh",
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

        # The container runs as root, so the ISO lands root-owned on the bind
        # mount. Make it world-readable and hand ownership to whoever owns the
        # mounted output directory, so the host user can use it without sudo.
        os.chmod(output_iso, 0o644)
        try:
            out_dir_stat = os.stat(os.path.dirname(output_iso))
            os.chown(output_iso, out_dir_stat.st_uid, out_dir_stat.st_gid)
        except OSError as e:
            print(f"Warning: could not chown {output_iso} to the output dir owner: {e}")

        print(f"Created {output_iso}")
        
    except KeyboardInterrupt:
        print("\nOperation cancelled by user")
        sys.exit(1)
    except Exception as e:
        print(f"Error: {e}")
        sys.exit(1)


if __name__ == "__main__":
    main()