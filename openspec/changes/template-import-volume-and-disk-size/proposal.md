## Why

A VirtualMachineTemplate cannot be built with a privilege-separated API token,
cannot set the template's disk size, and is never finished unless it also sets
`cloudInitConfig`. Clones of a cloud image therefore boot on the image's small
disk (about 5 GiB for Fedora Cloud), and templates whose cloud-init comes from
the clone (`spec.cloudInitData`) cannot be built at all.

Proxmox (checked in `qemu-server` `API2/Qemu.pm` and `pve-storage`
`Storage.pm`/`API2/Storage/Status.pm`):

- `import-from=<absolute path>` is root-only ("Only root can pass arbitrary
  filesystem paths"). Kubemox imports from `<local path>/template/iso/<file>`.
- `import-from=<volume ID>` works for tokens, but the volume must be `images`
  or `import` content, not `iso`.
- `download-url` accepts `content=import` (`.qcow2`, `.raw`, `.vmdk`, `.ova`
  and others), with `Datastore.AllocateTemplate`.

## What Changes

- `StorageDownloadURL.spec.content` accepts `import` as well as `iso` and
  `vztmpl`.
- VirtualMachineTemplate passes `imageConfig.content` to the StorageDownloadURL
  it creates, instead of always `iso`.
- For `import` content the template disk is imported by volume ID
  (`<storage>:import/<filename>`), which non-root tokens may use. `iso`
  content keeps the filesystem path (root-only), now on `imageConfig.storage`
  instead of always `local`.
- New optional `virtualMachineConfig.diskSize` (GiB): after the import and
  before conversion, `scsi0` is grown to that size. Clones get the size from
  their first boot.
- Disk import, boot order and conversion to a template run whether or not
  `cloudInitConfig` is set. The cloud-init drive and settings are applied only
  when `cloudInitConfig` is set.
- `additionalConfig` is applied by Go type, not by `TypeMeta.Kind`, which is
  empty on objects read through the controller-runtime client. A failed
  config call returns its error instead of dereferencing a nil task.

## Capabilities

### New Capabilities

- `vm-template-build`: how a VirtualMachineTemplate downloads its image,
  imports and sizes its disk, applies additional configuration and becomes a
  template, including with a non-root API token and without `cloudInitConfig`.

### Modified Capabilities

None. `openspec/specs/` has no existing specs.

## Impact

- API: `api/proxmox/v1alpha1/storagedownloadurl_types.go` (content pattern),
  `virtualmachinetemplate_types.go` (`diskSize`); regenerated CRDs, deepcopy
  and the Helm chart CRDs.
- Code: `internal/controller/proxmox/virtualmachinetemplate_controller.go`,
  `pkg/proxmox/virtualmachinetemplate.go`, `ApplyAdditionalConfiguration` in
  `pkg/proxmox/virtualmachine.go`.
- Proxmox token privileges for templates: `Datastore.AllocateTemplate` on the
  image storage (download), `Datastore.Audit`/`AllocateSpace` on it (import),
  plus the existing VM create/config privileges. No root token needed.
- Behaviour change: a template without `cloudInitConfig` now gets built and
  converted where before it stayed an unconverted VM with no disk.
