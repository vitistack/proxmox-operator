# Proxmox Operator

[![Go Report Card](https://goreportcard.com/badge/github.com/vitistack/proxmox-operator)](https://goreportcard.com/report/github.com/vitistack/proxmox-operator)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

A Kubernetes operator for managing Proxmox Virtual Environment (VE) virtual machines through declarative Machine CRDs. Built with the [vitistack/common](https://github.com/vitistack/common) framework.

## 🚀 Features

- **Declarative VM Management**: Create and manage Proxmox VMs using Kubernetes CRDs
- **Flexible Authentication**: Support for both username/password and API token authentication
- **Intelligent Node Selection**: Configurable strategies for VM placement across Proxmox cluster nodes
- **ISO Validation**: Pre-creation validation ensures ISOs exist before VM provisioning
- **Kubernetes-Native**: Full integration with Kubernetes controller-runtime and conditions
- **Configurable Storage & Network**: Environment-based configuration for different Proxmox setups
- **Production Ready**: Comprehensive error handling, logging, and monitoring

## 📋 Table of Contents

- [Prerequisites](#prerequisites)
- [Installation](#installation)
- [Configuration](#configuration)
- [Usage](#usage)
- [API Reference](#api-reference)
- [Development](#development)
- [Contributing](#contributing)
- [License](#license)

## 📋 Prerequisites

- Kubernetes cluster (v1.19+)
- Proxmox Virtual Environment (v6.0+)
- [vitistack/common](https://github.com/vitistack/common) CRDs installed
- Go 1.25+ (for development)

## 🛠️ Installation

### Using Helm (Recommended)

```bash
# Add the vitistack helm repository
helm repo add vitistack https://charts.vitistack.io
helm repo update

# Install the operator
helm install proxmox-operator vitistack/proxmox-operator
```

### Manual Installation

```bash
# Clone the repository
git clone https://github.com/vitistack/proxmox-operator.git
cd proxmox-operator

# Build and deploy
make deploy
```

## ⚙️ Configuration

### Environment Variables

Create a `.env` file or set environment variables:

```bash
# Required: Proxmox Connection
PROXMOX_ENDPOINT=https://your-proxmox-host:8006
PROXMOX_USERNAME=root@pam
PROXMOX_PASSWORD=your-proxmox-password

# Optional: Authentication (alternative to username/password)
# PROXMOX_TOKEN_ID=your-token-id
# PROXMOX_TOKEN_SECRET=your-token-secret

# Optional: TLS and Security
PROXMOX_INSECURE_TLS=false

# Optional: VM Management
PROXMOX_VM_ID_START=2000
PROXMOX_NODE_SELECTION=first
PROXMOX_ALLOWED_NODES=
PROXMOX_DEFAULT_STORAGE=local-lvm
PROXMOX_DEFAULT_NETWORK=vmbr0

# Optional: Logging
LOG_LEVEL=info
LOG_JSON=true
```

### Configuration Reference

| Variable | Default | Description |
|----------|---------|-------------|
| `PROXMOX_ENDPOINT` | - | Proxmox API endpoint URL (including /api2/json) |
| `PROXMOX_USERNAME` | - | Proxmox username (e.g., `root@pam`) |
| `PROXMOX_PASSWORD` | - | Proxmox password |
| `PROXMOX_TOKEN_ID` | - | API token ID (alternative auth) |
| `PROXMOX_TOKEN_SECRET` | - | API token secret (alternative auth) |
| `PROXMOX_INSECURE_TLS` | `false` | Skip TLS certificate verification |
| `PROXMOX_VM_ID_START` | `2000` | Starting VM ID for new machines |
| `PROXMOX_NODE_SELECTION` | `first` | Node selection strategy (`first`, `random`, `round-robin`) |
| `PROXMOX_ALLOWED_NODES` | - | Comma-separated list of allowed nodes |
| `PROXMOX_DEFAULT_STORAGE` | `local-lvm` | Default storage pool for VM disks |
| `PROXMOX_DEFAULT_NETWORK` | `vmbr0` | Default network bridge for VMs |
| `PROXMOX_DEFAULT_CPU_TYPE` | `x86-64-v2-AES` | CPU type for VMs (e.g., `host`, `kvm64`) |
| `PROXMOX_ENABLE_NUMA` | `false` | Enable NUMA for VMs |
| `PROXMOX_SCSI_CONTROLLER` | `virtio-scsi-single` | SCSI controller type |
| `PROXMOX_ENABLE_QEMU_AGENT` | `true` | Enable QEMU Guest Agent |
| `PROXMOX_START_ON_CREATE` | `true` | Start VM immediately after creation |

## 🚀 Usage

### Basic VM Creation

1. **Ensure MachineClasses are available**:
   ```bash
   kubectl get machineclasses
   ```

2. **Create a Machine**:
   ```yaml
   apiVersion: vitistack.io/v1alpha1
   kind: Machine
   metadata:
     name: my-proxmox-vm
     namespace: default
   spec:
     name: my-proxmox-vm
     machineClass: small  # Uses pre-installed machineclass
     os:
       family: linux
       distribution: debian
       version: "12"
       imageID: "local:iso/debian-12-netinst.iso"  # Must be uploaded to Proxmox
     network:
       assignPublicIP: false
       interfaces:
       - name: eth0
         primary: true
     disks:
     - name: root
       sizeGB: 20
       boot: true
     provider: proxmox
   ```

3. **Apply and monitor**:
   ```bash
   kubectl apply -f machine.yaml
   kubectl get machines -w
   ```

### Advanced Examples

See the [`examples/`](./examples/) directory for complete examples including:
- Debian netinstall VM creation
- MachineClass definitions
- Configuration templates

## 📖 API Reference

### Machine CRD

The operator extends the [vitistack/common Machine CRD](https://github.com/vitistack/common) with Proxmox-specific functionality.

#### Status Fields

The operator populates comprehensive status information from the Proxmox VM:

```yaml
status:
  phase: Running
  state: running              # VM state from Proxmox (running, stopped, paused)
  providerID: proxmox://pve1/2001
  machineID: "2001"
  provider: proxmox
  hostname: my-proxmox-vm
  region: pve1                # Proxmox node name
  zone: pve1                  # Proxmox node name
  cpus: 4
  memory: 4294967296          # Memory in bytes
  creationTime: "2025-12-07T10:00:00Z"
  lastUpdated: "2025-12-07T10:05:00Z"
  
  # Network information (requires QEMU Guest Agent)
  ipAddresses:
    - 192.168.1.100
  ipv6Addresses:
    - "2001:db8::1"
  privateIPAddresses:
    - 192.168.1.100
  publicIPAddresses: []
  networkInterfaces:
    - name: eth0
      macAddress: "BC:24:11:XX:XX:XX"
      ipAddresses:
        - 192.168.1.100
      ipv6Addresses:
        - "2001:db8::1"
      state: up
      type: ethernet
  
  conditions:
    - type: Ready
      status: "True"
      reason: VMRunning
      message: "VM successfully created and running"
      lastTransitionTime: "2025-12-07T10:00:00Z"
```

#### Status Field Reference

| Field | Description |
|-------|-------------|
| `phase` | Current lifecycle phase (`Pending`, `Creating`, `Running`, `Terminating`, `Terminated`, `Failed`) |
| `state` | VM state from Proxmox (`running`, `stopped`, `paused`) |
| `providerID` | Unique identifier in format `proxmox://node/vmid` |
| `machineID` | Proxmox VM ID |
| `provider` | Always `proxmox` for this operator |
| `hostname` | VM name from Proxmox |
| `region` / `zone` | Proxmox node name where VM is running |
| `cpus` | Number of CPU cores assigned |
| `memory` | Memory in bytes |
| `creationTime` | When the VM was created |
| `lastUpdated` | Last status sync timestamp |
| `ipAddresses` | All IPv4 addresses (requires QEMU Guest Agent) |
| `ipv6Addresses` | All IPv6 addresses (requires QEMU Guest Agent) |
| `privateIPAddresses` | RFC 1918 private IPv4 addresses |
| `publicIPAddresses` | Public IPv4 addresses |
| `networkInterfaces` | Detailed per-interface network info |

> **Note**: Network information (IP addresses, interfaces) requires the QEMU Guest Agent to be installed and running inside the VM. Enable it with `PROXMOX_ENABLE_QEMU_AGENT=true`.

#### Condition Types

- `Ready`: Overall machine readiness
  - `Creating`: VM creation in progress
  - `VMRunning`: VM successfully created
  - `ISOValidationFailed`: ISO not found in storage
  - `VMCreationFailed`: VM creation failed
  - `VMProvisioningFailed`: VM provisioning failed
  - `Stopping`: VM is being stopped
  - `Terminating`: VM deletion in progress
  - `Terminated`: VM successfully deleted
  - `DeletionFailed`: VM deletion failed
  - `VMDeleted`: VM was deleted externally from Proxmox

### Supported MachineClass Fields

- `cpu.cores`: Number of CPU cores
- `memory.quantity`: Memory allocation (e.g., `2Gi`, `4096Mi`)
- `machineProviders`: Must include `"proxmox"`

## 🔧 Development

### Local Development Setup

```bash
# Clone and setup
git clone https://github.com/vitistack/proxmox-operator.git
cd proxmox-operator

# Install dependencies
go mod download

# Run tests
make test

# Run linter
make lint

# Build
make build

# Run locally (requires Kubernetes cluster)
make run
```

### Project Structure

```
├── cmd/                    # Application entrypoints
├── internal/
│   ├── consts/            # Configuration constants
│   ├── controller/        # Kubernetes controllers
│   ├── services/          # Business logic services
│   └── settings/          # Configuration management
├── examples/              # Usage examples
├── hack/                  # Build and deployment scripts
├── config/                # Kubernetes manifests
└── test/                  # Integration tests
```

### Testing

```bash
# Unit tests
make test

# Linting
make lint

# Security scanning
make gosec

# Format code
make fmt

# Run all checks
make test && make lint && make gosec
```

## 🤝 Contributing

We welcome contributions! Please see our [Contributing Guide](CONTRIBUTING.md) for details.

### Development Workflow

1. Fork the repository
2. Create a feature branch: `git checkout -b feature/your-feature`
3. Make your changes with tests
4. Run the full test suite: `make ci`
5. Submit a pull request

### Code Standards

- Go 1.25+ compatible
- Follow standard Go formatting (`gofmt`)
- Comprehensive test coverage
- Security-first approach (passes `gosec`)
- Kubernetes API conventions

## 📄 License

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for details.

## 🙏 Acknowledgments

- [vitistack/common](https://github.com/vitistack/common) - Shared CRDs and utilities
- [go-proxmox](https://github.com/luthermonson/go-proxmox) - Proxmox API client
- [controller-runtime](https://github.com/kubernetes-sigs/controller-runtime) - Kubernetes operator framework

## 📞 Support

- **Issues**: [GitHub Issues](https://github.com/vitistack/proxmox-operator/issues)
- **Discussions**: [GitHub Discussions](https://github.com/vitistack/proxmox-operator/discussions)
- **Documentation**: [Wiki](https://github.com/vitistack/proxmox-operator/wiki)

---

**Happy ProxMoxing!** 🎉