## ADDED Requirements

### Requirement: Import content downloads
`StorageDownloadURL.spec.content` SHALL accept `iso`, `vztmpl` and `import`.
A VirtualMachineTemplate SHALL create its StorageDownloadURL with the content
type from `imageConfig.content`.

#### Scenario: Template image downloaded as import content
- **WHEN** a VirtualMachineTemplate sets `imageConfig.content: import` and `imageConfig.filename: Fedora-Cloud-Base-Generic-44-1.7.x86_64.qcow2`
- **THEN** its StorageDownloadURL requests `download-url` with `content=import` and that filename

#### Scenario: Unknown content rejected
- **WHEN** a StorageDownloadURL sets `content: images`
- **THEN** the API server rejects it by schema validation

### Requirement: Disk import by volume ID
For `imageConfig.content: import` the operator SHALL import the template's
`scsi0` with `import-from=<imageConfig.storage>:import/<imageConfig.filename>`,
so that a privilege-separated API token can build templates. For `iso`
content it SHALL keep importing from that storage's filesystem path, which
Proxmox allows only for root. It SHALL skip the import when `scsi0` is
already set.

#### Scenario: Non-root token imports an import-content image
- **WHEN** the connection uses a non-root token with access to the image storage and the image is `import` content
- **THEN** `scsi0` is created from `local:import/<filename>` on `virtualMachineConfig.storage`

#### Scenario: Existing iso-content templates
- **WHEN** `imageConfig.content` is `iso`
- **THEN** the import uses `<storage path>/template/iso/<filename>`, as before

### Requirement: Template disk size
VirtualMachineTemplate SHALL accept an optional
`virtualMachineConfig.diskSize` in GiB (minimum 1). When set and larger than
the imported `scsi0`, the operator SHALL grow `scsi0` to that size after the
import and before converting the VM to a template. It SHALL NOT shrink a disk
or resize a VM that is already a template.

#### Scenario: Clone gets the template size
- **WHEN** a template sets `diskSize: 40` and imports a 5 GiB image
- **THEN** the template's `scsi0` is 40 GiB before conversion, and its clones start with 40 GiB

#### Scenario: Smaller or equal size
- **WHEN** `diskSize` is not larger than the imported disk
- **THEN** no resize is requested

#### Scenario: Unset
- **WHEN** `diskSize` is unset
- **THEN** the disk keeps the image's size, as before

### Requirement: Templates without cloudInitConfig
The operator SHALL import the disk, set the boot order and convert the VM to
a template whether or not `cloudInitConfig` is set. It SHALL add the
Proxmox cloud-init drive and apply cloud-init settings only when
`cloudInitConfig` is set.

#### Scenario: Template for cloudInitData clones
- **WHEN** a VirtualMachineTemplate sets no `cloudInitConfig`
- **THEN** it becomes a Proxmox template with `scsi0` imported, boot order `scsi0` and no cloud-init drive

#### Scenario: Existing cloud-init templates
- **WHEN** a VirtualMachineTemplate sets `cloudInitConfig`
- **THEN** the cloud-init drive and settings are applied as before

### Requirement: Additional configuration by type
`additionalConfig` SHALL be applied to VirtualMachines and
VirtualMachineTemplates identified by their Go type, independent of
`TypeMeta`. A failed config request SHALL return its error.

#### Scenario: Object read without TypeMeta
- **WHEN** a VirtualMachineTemplate with `additionalConfig: {net0: "virtio,bridge=vmbr0,tag=116"}` is read through the controller-runtime client with empty `TypeMeta`
- **THEN** `net0` is set to that value on the template VM

#### Scenario: Proxmox rejects an option
- **WHEN** Proxmox rejects an `additionalConfig` key
- **THEN** the reconcile returns the error and the operator does not panic
