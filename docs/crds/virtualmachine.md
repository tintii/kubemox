# VirtualMachine

`VirtualMachine` is a way to create new VirtualMachines in Proxmox via operator. You can create `VirtualMachine` resource and Kubemox will create it for you in Proxmox. `VirtualMachine` is also reconciled by the operator which means every change on `VirtualMachine` resource will be reflected to Proxmox as well. 

## Creating VirtualMachine

To create a new virtual machine in Proxmox, you need to create a `VirtualMachine` object. You can create two types of `VirtualMachine` objects. You can create a new virtual machine from scratch or you can create one from a template.

- Creating a new virtual machine from a template

```yaml
# This manifest is used to create a Virtual Machine from an existing template.
cat <<EOF | kubectl apply -f -
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachine
metadata:
  name: virtualmachine-sample-clone
spec:
  connectionRef:
    name: proxmox-connection-sample
  name: virtualmachine-sample-clone
  # Name of the node where the VM will be created
  nodeName: lowtower
  template:
    # Name of the template to be cloned
    name: centos-template-new
    # CPU cores to be allocated to the VM
    cores: 2
    # CPU sockets to be allocated to the VM
    socket: 1
    # Memory to be allocated to the VM
    memory: 4096 # As MB
    # Deletion protection is whether to delete VM from Proxmox or not
    deleteProtection: false
    # VM should be started any time found in stopped state
    enableAutostart: true
    # Disk used by the VM
    disk: 
      - storage: nvme 
        size: 50 # As GB
        device: scsi0
    # Network interfaces used by the VM
    network:
      - model: virtio
        bridge: vmbr0
EOF
```

- Creating a new virtual machine from scratch

```yaml
# This manifest is used to create a VirtualMachine from scratch.
cat <<EOF | kubectl apply -f -
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachine
metadata:
  labels:
  name: virtualmachine-sample-scratch
spec:
  connectionRef:
    name: proxmox-connection-sample
  name: virtualmachine-sample-scratch
  # Name of the node where the VM will be created
  nodeName: lowtower
  # Spec for the VM
  vmSpec: 
    cores: 2
    memory: 4096
    disk: 
      name: scsi0 
      value: "local-lvm:40"
    network:
      name: net0
      value: virtio,bridge=vmbr0
    osImage:
      name: ide2 
      value: local:iso/ubuntu-23.04-live-server-amd64.iso
EOF
```

## Advanced Configuration

You can also configure advanced settings for the virtual machine object. You can set the following fields in the `VirtualMachine` object.

- Create a new virtual machine from a template

```yaml
# This manifest is used to create a Virtual Machine from an existing template with advanced configuration.
cat <<EOF | kubectl apply -f -
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachine
metadata:
  name: virtualmachine-sample-clone-new
  namespace: default
spec:
  connectionRef:
    name: proxmox-connection-sample
  name: virtualmachine-sample-clone-new
  nodeName: lowtower
  deletionProtection: false
  enableAutoStart: true
  template:
    socket: 1
    cores: 2
    disk:
    - device: scsi0
      size: 60
      storage: local-lvm
    - device: scsi1
      size: 20
      storage: local-lvm
    memory: 4096
    name: fedora-template
    network:
    - bridge: vmbr0
      model: virtio
    pciDevices:
      - device: "c0-p0-o0-if0"
        type: "mapped"
      - device: "0000:03:00.2"
        type: "raw"
        primaryGPU: true
        pcie: true
  additionalConfig:
    balloon: "0"
    cpu: host
    vga: type=virtio,memory=16
EOF
```

- Create a new virtual machine from scratch

```yaml
# This manifest is used to create a VirtualMachine from scratch with advanced configuration.
cat <<EOF | kubectl apply -f -
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachine
metadata:
  labels:
  name: virtualmachine-sample-scratch
spec:
  connectionRef:
    name: proxmox-connection-sample
  name: virtualmachine-sample-scratch
  # Name of the node where the VM will be created
  nodeName: lowtower 
  # Spec for the VM
  vmSpec: 
    socket: 1
    cores: 2
    memory: 4096
    disk: 
      - storage: local-lvm
        size: 60
        device: scsi0
    network:
      - model: virtio
        bridge: vmbr0
    pciDevices:
      - device: "c0-p0-o0-if1"
        type: "mapped"
    osImage:
      name: ide0
      value: local:iso/ubuntu-cloud-21.04-amd64.iso
  cloudInitConfig:
    user: "ubuntu"
    passwordFrom: 
        name: cloudinit-secret
        key: password
    upgradePackages: true
    custom:
      userData: "ubuntu-cloud-config-workers.yaml" # That should be inside snippets folder in Proxmox node
EOF
```

## Cloud-init data from Secrets and ConfigMaps

`spec.cloudInitData` delivers per-VM cloud-init documents held in Kubernetes,
for both template clones and VMs created from scratch. Each of `userData`
(required), `vendorData`, `metaData` and `networkConfig` references one key of
a Secret (`secretKeyRef`) or a ConfigMap (`configMapKeyRef`) in the
VirtualMachine's own namespace.

The operator renders the documents into a NoCloud ISO (volume label `cidata`),
uploads it through the Proxmox API as `iso` content and attaches it as a CD-ROM
on `device` (default `ide3`) before the VM first starts. No snippet storage or
SSH access to the Proxmox host is needed.

- When `metaData` is unset, `instance-id` and `local-hostname` are the VM name.
- Any Proxmox-generated cloud-init drive is removed, so the guest sees one
  `cidata` volume. Do not combine with `cloudInitConfig` or `cicustom`.
- The ISO is named `kubemox-ci-<vmid>-<hash>.iso`. When a referenced value
  changes, the next reconcile attaches a new ISO and deletes the old one. The
  VM is not rebooted, and cloud-init only reads the new data when the VM is
  next provisioned.
- Deleting the VirtualMachine deletes its ISOs, unless deletion protection
  keeps the VM.
- If a referenced object or key is missing, the VM is not started and a
  Warning event names the reference.

Requirements:

- The image needs cloud-init with the NoCloud datasource.
- The Proxmox API token needs `Datastore.AllocateSpace` and `Datastore.Audit`
  on the ISO storage.

The rendered documents, Secret values included, are stored as an ISO on that
storage and are readable by anyone with access to it. Use a dedicated
`storage` with restricted permissions for secrets.

```yaml
apiVersion: proxmox.alperen.cloud/v1alpha1
kind: VirtualMachine
metadata:
  name: k3s-agent-0
spec:
  name: k3s-agent-0
  nodeName: lowtower
  connectionRef:
    name: proxmoxconnection-sample
  template:
    name: fedora-template
  cloudInitData:
    userData:
      secretKeyRef:
        name: k3s-agent-cloudinit
        key: user-data
    vendorData:
      configMapKeyRef:
        name: node-prep
        key: vendor-data
```

See `examples/virtualmachinecloudinitdata.yaml` for the Secret and ConfigMap.
