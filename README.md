# RudderVirt Virt OS

RudderVirt Virt OS is a specialized operating system that provides a secure, containerized environment for hosting virtual machines on [ruddervirt.com](https://ruddervirt.com).

**Important**: This operating system is the foundation for running a private VM Deployment Zone on ruddervirt.com. However, it cannot be connected or used without coordination with us. Please contact us at [selfhosted@ruddervirt.com](mailto:selfhosted@ruddervirt.com) for more information.

## Prerequisites
- Install [Docker](https://www.docker.com/) or [Podman](https://podman.io/)
- Upload your SSH public keys to your GitHub profile

## Quick Start

Create a bootable installer ISO with your SSH keys embedded:

```bash
docker run -t -v "$PWD":/output ghcr.io/ruddervirt/ruddervirtvirt-os:latest /dev/sdX username
```

Replace:
- `/dev/sdX` with your target installation device (e.g., `/dev/sda`, `/dev/nvme0n1`)
- `username` with your GitHub username (used for SSH key retrieval)

The ISO will be created as `ruddervirtvirt-install.iso` in your current directory. Burn this onto a media or flash drive (we recommend [balenaEtcher](https://www.balena.io/etcher)). Then, boot into it on your hardware, and it will automatically install. **Warning:** This will overwrite any existing data on the drive.

### Target Hardware Requirements
- **CPU**: 
  - Bare metal x86_64 processor with VT-x/AMD-V support
  - Nested virtualization is not supported
- **Memory**:
  - Minimum 128GB RAM (more recommended)
- **Storage**: 
  - Minimum 500GB (more recommended)
  - SSD required, NVME recommended 
  - External SAN can also be used in lieu of local disks
- **Network**: 
  - Gigabit Ethernet connection with internet
  - A publicly resolvable DNS name
  - Firewall allows external TCP access to port 443 
