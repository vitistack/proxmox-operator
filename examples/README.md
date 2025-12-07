# Proxmox Operator Examples

This directory contains example configurations for the Proxmox Operator.

## Available MachineClasses

The cluster has the following machineclasses pre-installed:

- `small`: 2 CPU cores, 8Gi RAM (Standard)
- `medium`: 4 CPU cores, 16Gi RAM (Standard, Default)
- `large`: 4 CPU cores, 32Gi RAM (Standard)
- `largecpu`: 4 CPU cores, 8Gi RAM (CPU-focused)
- `largememory`: 4 CPU cores, 64Gi RAM (Memory-focused)
- `xlarge`: 4 CPU cores, 48Gi RAM (Memory-focused)
- `gpu`: 4 CPU cores, 16Gi RAM, 1024 GPU cores (GPU-enabled)

Use `kubectl get machineclasses` to see the current list.

## Machine Examples

### Debian Netinstall VM
- **File**: `machine-debian-netinstall.yaml`
- **Description**: Creates a VM that boots from the Debian 12 netinstall ISO using the `small` machineclass
- **Requirements**:
  - Uses the pre-installed `small` machineclass (2 CPU cores, 8Gi RAM)
  - **Debian ISO must be pre-uploaded to Proxmox storage**
- **Notes**:
  - Uses the Debian netinstall ISO for network-based installation
  - Boots from CD-ROM with the ISO image
  - Requires network access for package installation during setup

```bash
# First, upload the Debian ISO to Proxmox storage
# Download: https://deb.debian.org/debian/dists/bookworm/main/installer-amd64/current/images/netboot/mini.iso
# Upload to Proxmox via Web UI: Storage → ISO Images → Upload
# The ISO should be available as: local:iso/debian-12-netinst.iso

# Alternative: If your Proxmox supports HTTP URLs, you can modify the imageID
# to point directly to the ISO URL instead of uploading it first

# Then apply the machine
kubectl apply -f machine-debian-netinstall.yaml

# Watch the machine status
kubectl get machines -w
```

## Prerequisites

1. Ensure the Proxmox Operator is installed and running
2. Configure authentication (username/password or API token)
3. Verify Proxmox cluster connectivity
4. MachineClasses are pre-installed in the cluster
5. **Upload required ISOs to Proxmox storage** (HTTP URLs are validated but may not work for all Proxmox setups)

## New Features

### 🔍 ISO Validation
- **Pre-creation validation**: Checks if ISO exists in Proxmox storage before attempting VM creation
- **Early failure detection**: Fails fast with clear error messages if ISO is missing
- **HTTP URL support**: Validates local storage paths; skips validation for HTTP URLs

### 📊 Enhanced Status Conditions
- **Kubernetes-style conditions**: Proper `status.conditions[]` with type, status, reason, and message
- **Condition types**:
  - `Ready`: Overall machine readiness
  - Tracks creation progress, failures, and successful completion
- **Sorted conditions**: Conditions are properly managed and updated

### ⚙️ Configurable Storage & Network
- **Environment variables**:
  - `PROXMOX_DEFAULT_STORAGE`: Default storage pool (default: `local-lvm`)
  - `PROXMOX_DEFAULT_NETWORK`: Default network bridge (default: `vmbr0`)
- **Flexible deployment**: No more hardcoded values for different Proxmox setups

## Configuration

Update the following environment variables or `.env` file:

```bash
# Required
PROXMOX_ENDPOINT=https://your-proxmox-host:8006/api2/json
PROXMOX_USERNAME=root@pam  # or use token auth
PROXMOX_PASSWORD=your-password
# PROXMOX_TOKEN_ID=your-token-id
# PROXMOX_TOKEN_SECRET=your-token-secret

# Optional
PROXMOX_INSECURE_TLS=false
PROXMOX_NODE_SELECTION=first      # first, random, round-robin
PROXMOX_ALLOWED_NODES=            # comma-separated node names
PROXMOX_DEFAULT_STORAGE=local-lvm # default storage pool
PROXMOX_DEFAULT_NETWORK=vmbr0     # default network bridge
```