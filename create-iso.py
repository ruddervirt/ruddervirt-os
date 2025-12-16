#!/usr/bin/env python3

import sys
import os
import subprocess
import argparse
import urllib.request
import urllib.error
import tempfile
import glob
from passlib.hash import sha512_crypt
from jinja2 import Template
from pathlib import Path

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
    *,
    password_hash: str,
    ssh_keys: list[str] | None = None,
    is_ruddervirt: bool,
) -> str:
    with open(butane_file, 'r') as f:
        content = f.read()

    template = Template(content)
    rendered_content = template.render(
        password_hash=password_hash,
        ssh_keys=ssh_keys or [],
        is_ruddervirt=is_ruddervirt,
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


def main():
    parser = argparse.ArgumentParser(description="Create CoreOS installation ISO with embedded ignition config")
    parser.add_argument("install_disk", help="Target installation disk device")
    parser.add_argument("password", help="Plaintext password to set for the installed OS")
    parser.add_argument("--github-user", help="GitHub username to fetch SSH keys from (optional)")
    parser.add_argument("--not-self-hosted", action='store_true', help="Used for ruddervirt-managed servers")
    
    args = parser.parse_args()
    
    input_butane = "server.bu.j2"
    install_disk = args.install_disk
    password = args.password
    is_ruddervirt = args.not_self_hosted
    github_user = args.github_user
    output_ignition = Path(input_butane).with_suffix('.ign')
    output_iso = "/output/ruddervirtvirt-install.iso"
    fedora_iso = "fedora-coreos.iso"    
    temp_butane_file = None
    
    try:
        print("Hashing password")
        password_hash = hash_password_sha512(password)

        ssh_keys: list[str] = []
        if github_user:
            print(f"Fetching SSH keys for GitHub user: {github_user}")
            ssh_keys = fetch_github_ssh_keys(github_user)
            if not ssh_keys:
                raise ValueError(f"Error: No SSH keys found for GitHub user '{github_user}'. Aborting.")

        temp_butane_file = template_butane(
            input_butane,
            password_hash=password_hash,
            ssh_keys=ssh_keys,
            is_ruddervirt=is_ruddervirt,
        )
        input_butane = temp_butane_file

        print("Generating ignition")
        run_command([
            "butane", "--pretty", "--strict", input_butane, "--output", str(output_ignition)
        ])
        
        run_command(["ignition-validate", str(output_ignition)])
        
        if not os.path.exists(fedora_iso):
            print("Downloading Fedora CoreOS")
            run_command([
                "coreos-installer", "download", "-f", "iso", "--decompress", "--architecture", "x86_64"
            ])
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
        
        print("Embedding ignition")
        run_command([
            "coreos-installer", "iso", "customize",
            "--dest-ignition", str(output_ignition),
            "--dest-device", install_disk,
            "-o", output_iso,
            fedora_iso
        ])
        
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