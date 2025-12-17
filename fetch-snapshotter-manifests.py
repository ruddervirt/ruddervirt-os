#!/usr/bin/env python3
"""
Fetch and combine external-snapshotter manifests from GitHub.
This script should be run once to generate snapshotter-manifests.yaml
which is then committed to the repository.
"""

import os
import subprocess
import tempfile
import shutil
import argparse


def fetch_and_combine_snapshotter_manifests(csi_snapshot_version: str = "v8.2.0") -> str:
    """
    Fetch external-snapshotter repo and combine CRD and snapshot-controller manifests.
    Returns the combined YAML content as a string.
    """
    temp_dir = tempfile.mkdtemp(prefix='snapshotter_')
    try:
        # Clone the repo
        print(f"Cloning external-snapshotter repository (version: {csi_snapshot_version})...")
        subprocess.run(
            ["git", "clone", "https://github.com/kubernetes-csi/external-snapshotter.git", temp_dir],
            check=True,
            capture_output=True
        )
        
        # Checkout specific version
        print(f"Checking out version {csi_snapshot_version}...")
        subprocess.run(
            ["git", "checkout", csi_snapshot_version],
            cwd=temp_dir,
            check=True,
            capture_output=True
        )
        
        combined_manifests = []
        
        # Collect all YAML files from client/config/crd
        crd_path = os.path.join(temp_dir, "client/config/crd")
        if os.path.exists(crd_path):
            print(f"Processing CRD manifests from {crd_path}...")
            for root, dirs, files in os.walk(crd_path):
                for file in sorted(files):
                    if file.endswith('.yaml') or file.endswith('.yml'):
                        file_path = os.path.join(root, file)
                        with open(file_path, 'r') as f:
                            content = f.read().strip()
                            if content:
                                combined_manifests.append(content)
        
        # Collect all YAML files from deploy/kubernetes/snapshot-controller
        controller_path = os.path.join(temp_dir, "deploy/kubernetes/snapshot-controller")
        if os.path.exists(controller_path):
            print(f"Processing snapshot-controller manifests from {controller_path}...")
            for root, dirs, files in os.walk(controller_path):
                for file in sorted(files):
                    if file.endswith('.yaml') or file.endswith('.yml'):
                        file_path = os.path.join(root, file)
                        with open(file_path, 'r') as f:
                            content = f.read().strip()
                            if content:
                                combined_manifests.append(content)
        
        # Combine with YAML document separators
        combined_content = "\n---\n".join(combined_manifests)
        print(f"Combined {len(combined_manifests)} manifest(s)")
        
        return combined_content
    
    finally:
        # Cleanup
        if os.path.exists(temp_dir):
            shutil.rmtree(temp_dir)
            print(f"Cleaned up temporary directory: {temp_dir}")


def main():
    parser = argparse.ArgumentParser(
        description="Fetch and combine external-snapshotter manifests"
    )
    parser.add_argument(
        "--version",
        default="v8.2.0",
        help="CSI snapshot controller version (default: v8.2.0)"
    )
    parser.add_argument(
        "--output",
        default="snapshotter-manifests.yaml",
        help="Output file path (default: snapshotter-manifests.yaml)"
    )
    
    args = parser.parse_args()
    
    try:
        print(f"Fetching and combining external-snapshotter manifests...")
        combined_manifest = fetch_and_combine_snapshotter_manifests(args.version)
        
        with open(args.output, 'w') as f:
            f.write(combined_manifest)
        
        print(f"Successfully saved manifests to: {args.output}")
        print(f"You can now commit this file to the repository.")
        
    except subprocess.CalledProcessError as e:
        print(f"Error: Failed to clone or checkout repository: {e}")
        exit(1)
    except Exception as e:
        print(f"Error: {e}")
        exit(1)


if __name__ == "__main__":
    main()
