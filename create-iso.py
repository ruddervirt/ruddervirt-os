#!/usr/bin/env python3

import sys
import os
import subprocess
import argparse
import urllib.request
import urllib.error
import tempfile
import glob
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


def template_ssh_keys_into_butane(butane_file, ssh_keys):
    if not ssh_keys:
        print("No SSH keys to template, using original Butane file")
        return butane_file
    
    with open(butane_file, 'r') as f:
        content = f.read()
    
    template = Template(content)
    rendered_content = template.render(ssh_keys=ssh_keys)
    
    temp_fd, temp_path = tempfile.mkstemp(suffix='.bu', prefix='server_with_keys_')
    try:
        with os.fdopen(temp_fd, 'w') as f:
            f.write(rendered_content)
        print(f"Created temporary Butane file with SSH keys: {temp_path}")
        
        lines = rendered_content.split('\n')
        for i, line in enumerate(lines):
            print(f"  {i+1:2d}: {line}")
        
        return temp_path
    except Exception as e:
        os.close(temp_fd)
        if os.path.exists(temp_path):
            os.unlink(temp_path)
        raise e


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
    parser.add_argument("github_user", help="GitHub username to fetch SSH keys from")
    
    args = parser.parse_args()
    
    input_butane = "server.bu.j2"
    install_disk = args.install_disk
    github_user = args.github_user
    output_ignition = Path(input_butane).with_suffix('.ign')
    output_iso = "/output/ruddervirtvirt-install.iso"
    fedora_iso = "fedora-coreos.iso"    
    temp_butane_file = None
    
    try:
        print(f"Fetching SSH keys for GitHub user: {github_user}")
        ssh_keys = fetch_github_ssh_keys(github_user)
        if ssh_keys:
            temp_butane_file = template_ssh_keys_into_butane(input_butane, ssh_keys)
            input_butane = temp_butane_file
        else:
            print("Warning: No SSH keys found, proceeding with template defaults")
        
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