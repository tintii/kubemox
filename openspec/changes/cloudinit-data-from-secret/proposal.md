## Why

Kubemox can only point a VM at cloud-init snippets that already exist on the
Proxmox host (`cloudInitConfig.custom.*` and `cicustom` are snippet filenames),
and the Proxmox API cannot write snippet storage. Per-VM cloud-init that
carries secrets, such as a k3s join token, therefore has to be copied onto the
hypervisor by hand, outside GitOps and outside Kubernetes Secret handling.
The upstream TODO in `virtualmachinetemplate_types.go` asks for exactly this:
cloud-init data uploaded through the API from a Kubernetes source.

## What Changes

- New optional `spec.cloudInitData` on `VirtualMachine`, applying to both
  template clones and VMs created from scratch.
- `userData`, `vendorData`, `metaData` and `networkConfig` each reference a key
  in a Secret (`secretKeyRef`) or ConfigMap (`configMapKeyRef`) in the VM's
  namespace. `userData` is required; `metaData` defaults to a generated
  `instance-id` and `local-hostname`.
- The operator renders the data into a NoCloud ISO (volume label `cidata`),
  uploads it as `iso` content through the Proxmox API, and attaches it as a
  CD-ROM before the VM's first start. No SSH access to the host is needed.
- The ISO name carries a content hash. Changed data produces a new ISO, which
  replaces the old one on the VM; the old ISO is deleted.
- When `cloudInitData` is set, any Proxmox-generated cloud-init drive on the VM
  is removed, so the guest sees a single `cidata` source.
- The ISO is deleted when the VM is deleted (not when deletion protection keeps
  the VM).
- RBAC gains `get` on ConfigMaps. Secrets are already readable.
- No breaking changes. Existing `cloudInitConfig` and `additionalConfig.cicustom`
  behave as before.

## Capabilities

### New Capabilities
- `cloudinit-data-sources`: deliver per-VM cloud-init user, vendor, meta and
  network data from Kubernetes Secrets or ConfigMaps to Proxmox VMs as an
  API-uploaded NoCloud ISO, including update and cleanup.

### Modified Capabilities

None. The repository has no existing OpenSpec specs.

## Impact

- API: `api/proxmox/v1alpha1/virtualmachine_types.go` (new types, CEL
  validation), regenerated deepcopy and CRDs in `config/crd` and the chart.
- Code: `pkg/kubernetes` (ConfigMap/Secret key lookup in a given namespace),
  `pkg/proxmox` (ISO build, upload, attach, delete, cloud-init drive removal),
  `internal/controller/proxmox/virtualmachine_controller.go` (create, update and
  delete hooks).
- Dependencies: `github.com/diskfs/go-diskfs` moves from indirect to direct; it
  is already in the module graph through go-proxmox.
- Proxmox: the API token needs `Datastore.AllocateSpace` on the ISO storage.
  Rendered cloud-init (including secrets) is stored as an ISO on that storage.
- Out of scope here: `VirtualMachineTemplate`, `VirtualMachineSet`.
