## ADDED Requirements

### Requirement: Cloud-init data references
A `VirtualMachine` SHALL accept an optional `spec.cloudInitData` with the keys
`userData`, `vendorData`, `metaData` and `networkConfig`. Each key SHALL
reference exactly one of `secretKeyRef` or `configMapKeyRef` (name and key) in
the operator's namespace (`POD_NAMESPACE`); VirtualMachine is cluster-scoped. `userData` SHALL be required when
`cloudInitData` is set.

#### Scenario: Secret-backed user data
- **WHEN** a VirtualMachine sets `cloudInitData.userData.secretKeyRef` to an existing Secret and key
- **THEN** the operator uses that key's value as the VM's `user-data`

#### Scenario: Both sources on one key
- **WHEN** a key sets both `secretKeyRef` and `configMapKeyRef`
- **THEN** the API server rejects the VirtualMachine with a validation error

#### Scenario: Missing user data
- **WHEN** `cloudInitData` is set without `userData`
- **THEN** the API server rejects the VirtualMachine with a validation error

#### Scenario: Referenced key does not exist
- **WHEN** a referenced Secret, ConfigMap or key does not exist
- **THEN** the operator does not create or start the VM, records a Warning event naming the missing reference, and retries

### Requirement: Generated meta-data
When `metaData` is not referenced, the operator SHALL generate `meta-data`
with `instance-id` and `local-hostname` both set to the VirtualMachine's
`spec.name`.

#### Scenario: Default meta-data
- **WHEN** `cloudInitData` sets only `userData`
- **THEN** the ISO's `meta-data` contains `instance-id: <spec.name>` and `local-hostname: <spec.name>`

### Requirement: NoCloud ISO delivery through the Proxmox API
The operator SHALL render the resolved data into an ISO9660 image with volume
label `cidata` containing `user-data`, `meta-data` and, when referenced,
`vendor-data` and `network-config`. It SHALL upload the image as `iso` content
through the Proxmox API and attach it to the VM as a CD-ROM on the configured
device (default `ide3`). It SHALL NOT require SSH or filesystem access to the
Proxmox host.

#### Scenario: Clone from template
- **WHEN** a VirtualMachine with `spec.template` and `cloudInitData` is created
- **THEN** the ISO is uploaded and attached after the clone and before the VM is first started

#### Scenario: Create from scratch
- **WHEN** a VirtualMachine with `spec.vmSpec` and `cloudInitData` is created
- **THEN** the ISO is uploaded and attached before the VM is first started

#### Scenario: Storage selection
- **WHEN** `cloudInitData.storage` is set
- **THEN** the ISO is uploaded to that storage; otherwise to the first storage on the VM's node that accepts `iso` content

### Requirement: Single cloud-init source
When `cloudInitData` is set, the operator SHALL remove any Proxmox-generated
cloud-init drive from the VM, so the guest sees exactly one `cidata` volume.

#### Scenario: Template carries a cloud-init drive
- **WHEN** the cloned VM has a Proxmox cloud-init drive inherited from its template
- **THEN** the operator removes that drive before first start and attaches only its own ISO

### Requirement: Content-addressed updates
The ISO filename SHALL include the VM ID and a hash of the rendered content.
When the resolved data changes, the operator SHALL upload a new ISO, attach it
in place of the old one, and delete the old ISO. It SHALL NOT reboot the VM
for a data change.

#### Scenario: Unchanged data
- **WHEN** the VM is reconciled and the rendered content hash matches the attached ISO
- **THEN** no upload, attach or delete happens

#### Scenario: Secret value changes
- **WHEN** a referenced Secret value changes and the VM is next reconciled
- **THEN** a new ISO is attached, the previous ISO is deleted, the VM keeps running, and a Normal event records the replacement

### Requirement: Cleanup on deletion
When the operator deletes the Proxmox VM, it SHALL also delete the VM's
cloud-init ISO. When deletion protection keeps the VM, the ISO SHALL be kept.

#### Scenario: VM deleted
- **WHEN** a VirtualMachine with `cloudInitData` and no deletion protection is deleted
- **THEN** both the Proxmox VM and its cloud-init ISO are removed

#### Scenario: Deletion protection
- **WHEN** a VirtualMachine with `deletionProtection: true` is deleted
- **THEN** the Proxmox VM and its cloud-init ISO remain
