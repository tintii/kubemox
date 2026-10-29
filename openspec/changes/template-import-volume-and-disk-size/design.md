## Context

The VirtualMachineTemplate controller creates a StorageDownloadURL (always
`content: iso`), waits for the download, creates a VM, applies
`additionalConfig`, then, only inside `handleCloudInitOperations` and only
when `cloudInitConfig` is set: imports `scsi0` from
`<local path>/template/iso/<file>`, adds `ide2: local-lvm:cloudinit`, applies
cloud-init settings, sets `boot: order=scsi0` and converts to a template.

Proxmox constraints, checked in source: absolute `import-from` paths are
root-only; volume-ID imports need `images` or `import` content;
`download-url` supports `content=import`. go-proxmox v0.5.0 has
`VirtualMachine.ResizeDisk(ctx, disk, size)` returning a task.

`ApplyAdditionalConfiguration` branches on `GetObjectKind().Kind`, which is
empty for typed objects from the controller-runtime client, so it resolves an
empty VM name. On a `Config` error it continues with a nil task.

## Goals / Non-Goals

**Goals:**
- Build templates from cloud images with a non-root token.
- Template disk size set at build time, so clones need no post-boot resize.
- Templates for clones that bring their own cloud-init (`cloudInitData`).

**Non-Goals:**
- Changing an existing template's disk (templates are not resized).
- VLAN, CPU type or other new typed fields: `additionalConfig` covers them.
- Rebuilding a template when `imageConfig` changes.
- Upstream submission (possible later; kept upstream-shaped).

## Decisions

**1. Volume ID for `import` content, path kept for `iso`.** For
`content: import` the source is `<storage>:import/<file>`, which a token can
use. For `iso` the existing filesystem path stays, because Proxmox refuses
`iso` volumes as an import source; switching it would break today's
root-token templates. Alternative: volume ID for everything. Rejected: a
regression for existing users with no gain.

**2. Storage from `imageConfig.storage`.** Both import forms use the storage
the image was downloaded to (default `local`), not a hard-coded `local`.

**3. `diskSize` on `virtualMachineConfig`.** Next to `cores`/`memory`, an
`int` GiB with `Minimum=1`, optional. Resize runs after import, before
conversion, only when larger than the current size parsed from
`scsi0`'s `size=` (Proxmox rejects shrinking). Size is passed as `<n>G`
(absolute). Alternative: resize clones in the VM controller before first
start. Rejected: per-clone work and API calls, and a template that does not
reflect what clones get.

**4. Split image build from cloud-init.** `handleCloudInitOperations` becomes
the template build step: import, optional resize, cloud-init drive and config
only if `cloudInitConfig != nil`, boot order, convert. Each step is already
idempotent (import skips when `scsi0` is set, convert skips templates); the
resize step is skipped when the VM is already a template.

**5. Type switch in `ApplyAdditionalConfiguration`.** `switch r :=
vm.(type)` on `*VirtualMachine` / `*VirtualMachineTemplate`; unknown types
return an error. Return on `Config` error before using the task.

## Risks / Trade-offs

- [`iso` content still needs a root token] → Documented; use `import`
  content with a privilege-separated token.
- [A template that previously stalled without `cloudInitConfig` now
  completes] → Intended; it was unusable before.
- [Size parsing from `scsi0`] → Reuse the existing `G`/`M` parsing from
  `parseDiskConfiguration`; on parse failure skip the resize and log.
- [Resize task timing] → Wait for the task like import does before converting.

## Migration Plan

Release as fork tag `v0.7.1-tintii.3`. Existing VirtualMachineTemplates are
unaffected once they are templates (the build step is skipped by idempotency
checks). Rollback: previous image/chart; `diskSize` is ignored by older CRDs
only after the CRD is rolled back too.

## Open Questions

- Does `download-url` with `content=import` on PVE 9 accept the `.qcow2`
  filename unchanged? Source says yes (`IMPORT_EXT_RE`); verify on pve0.
- Fedora Cloud 44 image virtual size (expected ~5 GiB); verify with
  `qemu-img info` on pve0.
