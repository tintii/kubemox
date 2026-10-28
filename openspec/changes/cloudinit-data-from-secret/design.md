## Context

Kubemox v0.7.0 configures cloud-init two ways, and neither carries
Kubernetes-held data to the guest:

- `cloudInitConfig` maps to Proxmox's generated cloud-init drive (`ciuser`,
  `cipassword`, `sshkeys`, `ipconfig0`). For `VirtualMachine` it is only read on
  the from-scratch path, and `SetCloudInitConfig` is commented out in
  `handleCloudInitOperations`.
- `cloudInitConfig.custom.*` and `additionalConfig.cicustom` become
  `cicustom=user=local:snippets/<name>` (`pkg/proxmox/virtualmachinetemplate.go`).
  The file must already exist on the host, and the Proxmox upload API only
  accepts `iso`, `vztmpl` and `import` content.

go-proxmox v0.5.0 already contains a NoCloud ISO builder
(`VirtualMachine.CloudInit`, backed by go-diskfs ISO9660 with label
`cidata`). Its exported entry point fixes the filename (`user-data-<vmid>.iso`),
always uses the node's first ISO storage, rewrites the boot order, and offers
no delete.

## Goals / Non-Goals

**Goals:**
- Per-VM cloud-init from Secrets/ConfigMaps, delivered with only the API token.
- Applied before first boot on both create paths.
- Idempotent reconcile; changed data replaces the ISO; ISO removed with the VM.

**Non-Goals:**
- `VirtualMachineTemplate` and `VirtualMachineSet` support (follow-up; sets
  could pass the field through to generated VMs).
- Re-running cloud-init after a data change. cloud-init runs per instance; a
  change takes effect on reprovision, not on the running guest.
- Watching Secrets/ConfigMaps for immediate updates; changes are picked up on
  the next reconcile.
- Templating or merging cloud-init documents; values are used verbatim.

## Decisions

**1. New `spec.cloudInitData`, not an extension of `cloudInitConfig`.**
`cloudInitConfig` lives under `vmSpec` (scratch only) and describes Proxmox's
own drive. A top-level field applies to clones too and keeps the two
mechanisms separate. Alternative: turn `custom.*` into references, as the
upstream TODO sketches. Rejected for now because it changes the meaning of an
existing field from snippet filename to content.

```yaml
cloudInitData:
  userData:      { secretKeyRef: { name: k00w00-cloudinit, key: user-data } }
  vendorData:    { configMapKeyRef: { name: node-prep, key: vendor-data } }
  metaData:      {}            # optional, generated when absent
  networkConfig: {}            # optional
  storage: local               # optional, first iso-capable storage on the node
  device: ide3                 # optional
```

Each data key is a struct with optional `secretKeyRef`/`configMapKeyRef`
(`corev1.SecretKeySelector`/`ConfigMapKeySelector`) and a CEL rule requiring
exactly one.

**2. References resolve in the VirtualMachine's namespace.** That is standard
Kubernetes semantics and lets workloads keep their data next to their VM.
The existing `passwordFrom` reads from `POD_NAMESPACE`; this field
deliberately does not. The ClusterRole already grants Secret `get`;
ConfigMap `get` is added via a kubebuilder RBAC marker.

**3. Own small ISO builder in `pkg/proxmox`, reusing go-diskfs.** The
go-proxmox helper cannot choose storage or name and rewrites boot order. The
builder is ~50 lines over go-diskfs (already in the module graph) and mirrors
the helper's options (ISO9660 + Rock Ridge + Joliet, label `cidata`). Upload
and attach use existing go-proxmox calls (`Storage.UploadWithName`,
`VirtualMachine.Config`), and delete uses `Storage.DeleteContent`. The NoCloud
CD-ROM is not added to the boot order.

**4. Content-addressed filename `kubemox-ci-<vmid>-<sha256[:12]>.iso`.** The
hash covers all rendered files. Drift detection compares the attached
device's volume with the expected name; no new status field is needed. On
change: upload new, reconfigure the device, then delete the old volume.

**5. Remove Proxmox's cloud-init drive when `cloudInitData` is set.** Two
`cidata` volumes make the datasource ambiguous. The operator deletes any
config entry whose value is a `cloudinit` volume before attaching its ISO.

**6. Hook points.** `handleCreateFromTemplate` and `handleCreateFromScratch`
call the new step after clone/creation and additional config, before
`startCreatedVM`. The update path calls it in place of the reboot-based
`handleCloudInitOperations` behaviour when `cloudInitData` is set.
`DeleteVirtualMachine` deletes the ISO after the VM.

**7. Default meta-data.** `instance-id` and `local-hostname` are `spec.name`.
A stable instance ID means an ISO swap does not make cloud-init treat the
guest as a new instance on reboot.

## Risks / Trade-offs

- [Secrets at rest on Proxmox storage as a readable ISO] → Document it; allow
  a dedicated `storage` with restricted `Datastore.Audit`; use
  `vendorData`/ConfigMaps for non-secret preparation.
- [PVE upload may refuse to overwrite an existing filename] → Names are
  content-addressed; an identical name means identical content, so an existing
  file is treated as uploaded.
- [Orphaned ISOs if the operator crashes between upload and delete] → On each
  reconcile, delete `kubemox-ci-<vmid>-*` volumes other than the attached one.
- [Token lacks `Datastore.AllocateSpace` on ISO storage] → Surface the API
  error as a Warning event; document the privilege.
- [Template clones whose guest has no cloud-init NoCloud datasource] → Out of
  operator control; documented requirement of the image.

## Migration Plan

Additive API field; existing VMs are untouched. Rollout: build the fork image,
bump the chart's CRDs and image, then move a VM from
`additionalConfig.cicustom` to `cloudInitData`. That VM must be recreated for
cloud-init to consume the new source. Rollback: older operator versions ignore
the field (CRD pruning drops it on update), leaving the attached ISO in place.

## Open Questions

- Does Proxmox reject uploads of an existing filename, or overwrite? Determines
  whether the "treat as uploaded" path needs an explicit existence check first.
- Should `VirtualMachineSet` pass `cloudInitData` through in this change, given
  sets need per-VM `instance-id`/hostname?
- Upstream appetite: propose as an issue to `alperencelik/kubemox` before
  diverging further.
