## 1. API

- [x] 1.1 Add `CloudInitData`, `CloudInitDataSource` (`secretKeyRef`/`configMapKeyRef`) types and `spec.cloudInitData` to `VirtualMachineSpec`, with CEL rules: exactly one source per key, `userData` required
- [x] 1.2 Add defaults: `device: ide3`; `storage` optional
- [x] 1.3 Add kubebuilder RBAC marker for ConfigMap `get`
- [x] 1.4 Run `make generate manifests`; sync regenerated CRDs and ClusterRole into the Helm chart

## 2. Data resolution

- [x] 2.1 Add namespace-aware Secret and ConfigMap key lookups in `pkg/kubernetes`, returning a typed not-found error naming the reference
- [x] 2.2 Resolve all four keys for a VirtualMachine; generate default `meta-data` from `spec.name` when absent
- [x] 2.3 Unit test resolution with a fake clientset: Secret, ConfigMap, missing object, missing key, default meta-data

## 3. ISO build and Proxmox operations

- [x] 3.1 Promote `github.com/diskfs/go-diskfs` to a direct dependency
- [x] 3.2 Implement `BuildNoCloudISO(files) (path, sha256, error)` (ISO9660, Rock Ridge, Joliet, label `cidata`)
- [x] 3.3 Unit test: read the ISO back with go-diskfs; label `cidata`, expected files and contents; identical input gives identical hash
- [x] 3.4 Implement ensure-ISO: choose storage (explicit or first iso-capable on node), name `kubemox-ci-<vmid>-<hash12>.iso`, upload unless present, attach as `<device>: <storage>:iso/<name>,media=cdrom`
- [x] 3.5 Remove any `cloudinit` volume config entry from the VM before attaching
- [x] 3.6 Delete stale `kubemox-ci-<vmid>-*` volumes other than the attached one
- [x] 3.7 Implement delete-ISOs-for-VM for the deletion path

## 4. Controller

- [x] 4.1 Call ensure-ISO in `handleCreateFromTemplate` and `handleCreateFromScratch` before `startCreatedVM`; on resolution errors emit a Warning event and requeue without starting
- [x] 4.2 Call ensure-ISO on update when `cloudInitData` is set, without rebooting; emit a Normal event when the ISO is replaced
- [x] 4.3 Delete the VM's ISOs in `DeleteVirtualMachine`; skip when deletion protection keeps the VM

## 5. Verification and docs

- [ ] 5.1 Resolve the open question: does PVE 9 overwrite or reject an existing upload filename; adjust 3.4
- [ ] 5.2 Manual test on a PVE 9 host: clone a Fedora Cloud template with Secret user-data, confirm cloud-init ran from the ISO and the PVE cloud-init drive is gone
- [ ] 5.3 Manual test: change the Secret, confirm the ISO is swapped without reboot and the old ISO deleted; delete the VM, confirm the ISO is removed
- [x] 5.4 Add `examples/` manifest and a docs section covering the field, namespace semantics, required token privilege and secrets-at-rest caveat
- [x] 5.5 `make test` and `make lint` pass
