# Proxmox VE Plugin for Formae

Proxmox VE resource plugin for [Formae](https://github.com/platform-engineering-labs/formae). This plugin enables Formae to manage Proxmox Virtual Environment resources using the Proxmox REST API.

## Installation

```bash
# Install the plugin
make install
```

## Supported Resources

This plugin supports **4 Proxmox VE resource types**:

| Type | Discoverable | Extractable | Comment |
|------|--------------|-------------|---------|
| Proxmox::Compute::VM | Yes | Yes | QEMU/KVM virtual machines |
| Proxmox::Compute::LXC | Yes | Yes | LXC containers |
| Proxmox::Storage::Storage | Yes | Yes | Storage configuration (dir, nfs, cifs, lvm, zfs, etc.) |
| Proxmox::Network::Interface | Yes | Yes | Network interfaces (bridges, bonds, vlans, OVS) |

See [`schema/pkl/`](schema/pkl/) for the complete schema definitions.

## Configuration

### Target Configuration

Configure a Proxmox target in your Forma file:

```pkl
import "@formae/formae.pkl"
import "@proxmox/proxmox.pkl"

target: formae.Target = new formae.Target {
    label = "proxmox-target"
    config = new proxmox.Config {
        apiUrl = "https://pve.example.com:8006"
        node = "pve"  // Default node name
    }
}
```

### Credentials

This plugin uses API token authentication. Set the following environment variables:

```bash
export PROXMOX_API_URL="https://pve.example.com:8006"
export PROXMOX_TOKEN_ID="user@realm!tokenname"
export PROXMOX_SECRET="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
export PROXMOX_NODE="pve"
```

**Optional:**
```bash
export PROXMOX_INSECURE_SKIP_VERIFY="true"  # Skip TLS verification (not recommended for production)
```

**Getting API Token Credentials:**

1. Log in to Proxmox VE web interface
2. Navigate to Datacenter > Permissions > API Tokens
3. Click "Add" to create a new API token
4. Note the Token ID (format: `user@realm!tokenname`) and Secret (UUID)
5. Assign appropriate permissions to the token

**Required Permissions:**

For VM management:
- `VM.Allocate` - Create VMs
- `VM.Config.*` - Modify VM configuration
- `VM.PowerMgmt` - Start/stop VMs
- `VM.Audit` - Read VM status

For LXC container management:
- `VM.Allocate` - Create containers
- `VM.Config.*` - Modify container configuration
- `VM.PowerMgmt` - Start/stop containers
- `VM.Audit` - Read container status

For storage management:
- `Datastore.Allocate` - Create/modify storage configurations
- `Datastore.Audit` - Read storage configurations

For network management:
- `Sys.Modify` - Create/modify network interfaces
- `Sys.Audit` - Read network configuration

## Examples

See the [examples/](examples/) directory for usage examples.

### Create a VM

```pkl
amends "@formae/forma.pkl"
import "@formae/formae.pkl"
import "@proxmox/proxmox.pkl"
import "@proxmox/compute/vm.pkl" as vm

forma {
    new formae.Stack {
        label = "my-stack"
    }

    new formae.Target {
        label = "proxmox"
        config = new proxmox.Config {
            apiUrl = read?("env:PROXMOX_API_URL")
            node = read?("env:PROXMOX_NODE")
        }
    }

    new vm.VM {
        label = "web-server"
        vmid = 100
        name = "web-server"
        cores = 2
        memory = 2048
        ostype = "l26"
        net0 = "virtio,bridge=vmbr0"
        scsi0 = "local-lvm:32"
        boot = "order=scsi0"
    }
}
```

### Create an LXC Container

```pkl
amends "@formae/forma.pkl"
import "@formae/formae.pkl"
import "@proxmox/proxmox.pkl"
import "@proxmox/compute/lxc.pkl" as lxc

forma {
    new formae.Stack {
        label = "my-stack"
    }

    new formae.Target {
        label = "proxmox"
        config = new proxmox.Config {
            apiUrl = read?("env:PROXMOX_API_URL")
            node = read?("env:PROXMOX_NODE")
        }
    }

    new lxc.LXC {
        label = "app-container"
        vmid = 200
        ostemplate = "local:vztmpl/debian-12-standard_12.2-1_amd64.tar.zst"
        hostname = "app-container"
        rootfs = "local-lvm:8"
        cores = 1
        memory = 512
        net0 = "name=eth0,bridge=vmbr0,ip=dhcp"
        unprivileged = true
        start = true
    }
}
```

### Create a Storage Configuration

```pkl
amends "@formae/forma.pkl"
import "@formae/formae.pkl"
import "@proxmox/proxmox.pkl"
import "@proxmox/storage/storage.pkl" as storage

forma {
    new formae.Stack {
        label = "my-stack"
    }

    new formae.Target {
        label = "proxmox"
        config = new proxmox.Config {
            apiUrl = read?("env:PROXMOX_API_URL")
            node = read?("env:PROXMOX_NODE")
        }
    }

    new storage.Storage {
        label = "nfs-images"
        storage = "nfs-images"
        `type` = "nfs"
        server = "nas.example.com"
        export = "/volume1/proxmox"
        content = "images,rootdir"
        shared = true
    }
}
```

### Create a Network Bridge

```pkl
amends "@formae/forma.pkl"
import "@formae/formae.pkl"
import "@proxmox/proxmox.pkl"
import "@proxmox/network/network.pkl" as network

forma {
    new formae.Stack {
        label = "my-stack"
    }

    new formae.Target {
        label = "proxmox"
        config = new proxmox.Config {
            apiUrl = read?("env:PROXMOX_API_URL")
            node = read?("env:PROXMOX_NODE")
        }
    }

    new network.Interface {
        label = "vmbr0"
        iface = "vmbr0"
        `type` = "bridge"
        bridge_ports = "enp1s0"
        bridge_vlan_aware = true
        autostart = true
        cidr = "192.168.1.10/24"
        gateway = "192.168.1.1"
    }
}
```

### Apply Resources

```bash
# Evaluate an example
formae eval examples/vm.pkl

# Apply resources (dry-run first)
formae apply --mode reconcile --simulate examples/vm.pkl

# Apply resources
formae apply --mode reconcile --watch examples/vm.pkl
```

## Development

### Prerequisites

- Go 1.25+
- [Pkl CLI](https://pkl-lang.org/main/current/pkl-cli/index.html) 0.30+
- Proxmox VE cluster with API access (for integration/conformance testing)

### Building

```bash
make build      # Build plugin binary
make test       # Run tests
make lint       # Run linter
make install    # Build + install locally
```

### Schema Verification

```bash
make verify-schema  # Verify PKL schema files
```

### Local Testing

```bash
# Install plugin locally
make install

# Start formae agent
formae agent start

# Apply example resources
formae apply --mode reconcile --watch examples/vm.pkl
```

### Conformance Testing

Run the full CRUD lifecycle + discovery tests:

```bash
# Set credentials
export PROXMOX_API_URL="https://pve.example.com:8006"
export PROXMOX_TOKEN_ID="user@realm!tokenname"
export PROXMOX_SECRET="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
export PROXMOX_NODE="pve"

# Run conformance tests
make conformance-test
```

## License

This plugin is licensed under the [Apache License 2.0](LICENSE).
