## 1. API

- [x] 1.1 Allow `import` in the `StorageDownloadURLContent.Content` validation pattern
- [x] 1.2 Add optional `DiskSize int` (`json:"diskSize,omitempty"`, `Minimum=1`, GiB) to template `VirtualMachineConfig`
- [x] 1.3 Run `make generate manifests chart`; confirm the CRDs in `config/` and `charts/kubemox/` carry both changes

## 2. Image download and import

- [x] 2.1 `createStorageDownloadURLCR`: use `vmTemplate.Spec.ImageConfig.Content` instead of the literal `iso`
- [x] 2.2 `ImportDiskToVM`: take the image storage and content; for `import` use `<storage>:import/<file>`, for `iso` keep `<storage path>/template/iso/<file>` on that storage
- [x] 2.3 Unit test the import source string for both content types

## 3. Template build

- [x] 3.1 Add `ResizeTemplateDisk`: skip when unset, already a template, or not larger than `scsi0`'s current size; otherwise `ResizeDisk(scsi0, "<n>G")` and wait for the task
- [x] 3.2 Restructure `handleCloudInitOperations` into the build step: import, resize, cloud-init drive and settings only when `cloudInitConfig` is set, boot order, convert
- [x] 3.3 Unit test the resize decision (unset, smaller, equal, larger, `M` sizes, parse failure)

## 4. additionalConfig

- [x] 4.1 `ApplyAdditionalConfiguration`: type switch on `*VirtualMachine` / `*VirtualMachineTemplate`, error on others; return on `Config` error before using the task
- [x] 4.2 Unit test name/node/config extraction for both types with empty `TypeMeta`

## 5. Docs and checks

- [x] 5.1 Document `content: import`, `diskSize`, templates without `cloudInitConfig` and token privileges in `docs/crds/` (template and StorageDownloadURL pages)
- [x] 5.2 Add an example `examples/virtualMachineTemplateImport.yaml` (Fedora Cloud qcow2, `import`, `diskSize`, VLAN via `additionalConfig`)
- [x] 5.3 `make test` and golangci-lint pass
- [ ] 5.4 Manual test on pve0 with the `nick@pam!one` token: template builds, `scsi0` is the requested size, `net0` has `tag=116`, no `ide2` cloud-init drive, VM is a template; resolve the design's open questions
