# RudderVirt OS

RudderVirt OS is a purpose-built operating system based on Fedora CoreOS that provides a secure, containerized environment for hosting virtual machines. It integrates seamlessly with the RudderVirt platform for remote VM management and orchestration.

## Quick Start

Create a bootable installer ISO with your SSH keys embedded:

```bash
docker run -t -v "$PWD":/output ghcr.io/ruddervirt/ruddervirtvirt-os:v1.0.0 /dev/sdX username
```

Replace:
- `/dev/sdX` with your target installation device (e.g., `/dev/sda`, `/dev/nvme0n1`)
- `username` with your GitHub username (for SSH key retrieval)

The ISO will be created as `ruddervirtvirt-install.iso` in your current directory.

## Prerequisites

### Host System Requirements
- **Operating System**: Linux, macOS, or Windows with WSL2
- **Container Runtime**: Docker or Podman
- **Architecture**: x86_64 (AMD64)

### GitHub Account Setup
- SSH public keys must be uploaded to your GitHub profile
- Keys are automatically retrieved from `https://github.com/USERNAME.keys`
- Ensure at least one SSH key is configured for secure access

### Target Hardware Requirements
- **CPU**: Bare metal x86_64 processor with VT-x/AMD-V support
- **Memory**: Minimum 128GB RAM (256GB+ recommended for production)
- **Storage**: 500GB+ NVMe SSD (1TB+ recommended)
- **Network**: Gigabit Ethernet connection
- **Note**: Nested virtualization is not supported


### Building from Source
```bash
git clone https://github.com/ruddervirt/ruddervirtvirt-os.git
cd ruddervirtvirt-os
docker build -t ruddervirtvirt-os .
docker run -t -v "$PWD":/output ruddervirtvirt-os /dev/sda yourusername
```
