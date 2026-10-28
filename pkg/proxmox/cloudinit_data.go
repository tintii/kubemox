package proxmox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
	"github.com/luthermonson/go-proxmox"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/kubernetes"
)

// NoCloud file names, as cloud-init expects them on the cidata volume.
const (
	ciUserData      = "user-data"
	ciVendorData    = "vendor-data"
	ciMetaData      = "meta-data"
	ciNetworkConfig = "network-config"

	// ciVolumeLabel is the volume label cloud-init's NoCloud datasource looks for.
	ciVolumeLabel = "cidata"
	ciBlockSize   = 2048
)

// ResolveCloudInitData reads the documents referenced by vm.Spec.CloudInitData
// from the VirtualMachine's namespace and returns them keyed by NoCloud file
// name. Unreferenced optional documents are omitted, except meta-data, which
// defaults to the VM name as instance-id and local-hostname.
func ResolveCloudInitData(ctx context.Context, r client.Reader,
	vm *proxmoxv1alpha1.VirtualMachine) (map[string]string, error) {
	data := vm.Spec.CloudInitData
	if data == nil {
		return nil, nil
	}
	sources := map[string]*proxmoxv1alpha1.CloudInitDataSource{
		ciUserData:      &data.UserData,
		ciVendorData:    data.VendorData,
		ciMetaData:      data.MetaData,
		ciNetworkConfig: data.NetworkConfig,
	}
	files := map[string]string{}
	for name, src := range sources {
		if src == nil {
			continue
		}
		value, err := resolveCloudInitSource(ctx, r, vm.Namespace, src)
		if err != nil {
			return nil, fmt.Errorf("cloud-init %s: %w", name, err)
		}
		files[name] = value
	}
	if data.MetaData == nil {
		files[ciMetaData] = fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n", vm.Spec.Name, vm.Spec.Name)
	}
	return files, nil
}

func resolveCloudInitSource(ctx context.Context, r client.Reader, namespace string,
	src *proxmoxv1alpha1.CloudInitDataSource) (string, error) {
	switch {
	case src.SecretKeyRef != nil:
		return kubernetes.GetSecretKey(ctx, r, namespace, src.SecretKeyRef)
	case src.ConfigMapKeyRef != nil:
		return kubernetes.GetConfigMapKey(ctx, r, namespace, src.ConfigMapKeyRef)
	default:
		// The CRD's CEL rule makes this unreachable for admitted objects.
		return "", fmt.Errorf("neither secretKeyRef nor configMapKeyRef is set")
	}
}

// BuildNoCloudISO writes files into a new ISO9660 image labelled cidata, the
// same layout go-proxmox uses for its own cloud-init ISOs. Empty documents are
// left out. It returns the image path, which the caller removes, and a hash of
// the written documents: the image itself is not byte-stable across builds.
func BuildNoCloudISO(files map[string]string) (isoPath, contentHash string, err error) {
	dir, err := os.MkdirTemp("", "kubemox-cloudinit-")
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	isoPath = filepath.Join(dir, "cidata.iso")
	f, err := os.Create(isoPath)
	if err != nil {
		return "", "", err
	}
	if err = f.Close(); err != nil {
		return "", "", err
	}
	backend, err := file.OpenFromPath(isoPath, false)
	if err != nil {
		return "", "", err
	}
	defer func() {
		if cerr := backend.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	fs, err := iso9660.Create(backend, 0, 0, ciBlockSize, "")
	if err != nil {
		return "", "", err
	}

	names := make([]string, 0, len(files))
	for name, content := range files {
		if content != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	hash := sha256.New()
	for _, name := range names {
		// Name and content are NUL-separated so different splits cannot collide.
		hash.Write([]byte(name + "\x00" + files[name] + "\x00"))
		rw, oerr := fs.OpenFile("/"+name, os.O_CREATE|os.O_RDWR)
		if oerr != nil {
			return "", "", oerr
		}
		if _, err = rw.Write([]byte(files[name])); err != nil {
			return "", "", err
		}
		// go-diskfs records a file's size when its handle closes, so close
		// before Finalize.
		if err = rw.Close(); err != nil {
			return "", "", err
		}
	}
	if err = fs.Finalize(iso9660.FinalizeOptions{
		RockRidge:        true,
		Joliet:           true,
		VolumeIdentifier: ciVolumeLabel,
	}); err != nil {
		return "", "", err
	}
	return isoPath, hex.EncodeToString(hash.Sum(nil)), nil
}

// ciISOPrefix starts the name of every ISO this operator uploads for a VM; the
// rest of the name is the content hash, so a name identifies its content.
func ciISOPrefix(vmID int) string { return fmt.Sprintf("kubemox-ci-%d-", vmID) }

// pveCloudInitVolume matches a Proxmox-generated cloud-init drive, e.g.
// "local-lvm:vm-100-cloudinit,media=cdrom".
var pveCloudInitVolume = regexp.MustCompile(`:(\d+/)?vm-\d+-cloudinit`)

// ciTaskTimeout bounds each upload, config and delete task, in seconds.
const ciTaskTimeout = 120

// EnsureCloudInitISO makes the VM's cloud-init ISO match files: it uploads a
// new ISO when the content changed, attaches it on data.Device, removes any
// Proxmox-generated cloud-init drive, and deletes this VM's superseded ISOs.
// It never reboots the VM. It returns the volume it replaced, or "" when the
// device already held the right ISO or held none of ours.
func (pc *ProxmoxClient) EnsureCloudInitISO(ctx context.Context, vmName, nodeName string,
	data *proxmoxv1alpha1.CloudInitData, files map[string]string) (replaced string, err error) {
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return "", err
	}
	vmID, err := pc.getVMID(vmName, nodeName)
	if err != nil {
		return "", err
	}
	// Read the VM directly: the cached copy may predate an earlier config change.
	vm, err := node.VirtualMachine(ctx, vmID)
	if err != nil {
		return "", err
	}
	storage, err := ciStorage(ctx, node, data.Storage)
	if err != nil {
		return "", err
	}

	isoPath, hash, err := BuildNoCloudISO(files)
	if err != nil {
		return "", fmt.Errorf("building cloud-init ISO: %w", err)
	}
	defer func() { _ = os.RemoveAll(filepath.Dir(isoPath)) }()
	name := ciISOPrefix(vmID) + hash[:12] + ".iso"
	volid := fmt.Sprintf("%s:iso/%s", storage.Name, name)

	ours, err := ciISOs(ctx, storage, vmID)
	if err != nil {
		return "", err
	}
	if !slices.Contains(ours, volid) {
		task, uerr := storage.UploadWithName("iso", isoPath, name)
		if uerr != nil {
			return "", fmt.Errorf("uploading %s: %w", volid, uerr)
		}
		if err = task.WaitFor(ctx, ciTaskTimeout); err != nil {
			return "", fmt.Errorf("uploading %s: %w", volid, err)
		}
	}

	var options []proxmox.VirtualMachineOption
	var drop []string
	drives := maps.Clone(vm.VirtualMachineConfig.MergeIDEs())
	maps.Copy(drives, vm.VirtualMachineConfig.MergeSATAs())
	maps.Copy(drives, vm.VirtualMachineConfig.MergeSCSIs())
	for key, value := range drives {
		if key != data.Device && pveCloudInitVolume.MatchString(value) {
			drop = append(drop, key)
		}
	}
	current := drives[data.Device]
	want := volid + ",media=cdrom"
	if current != want {
		options = append(options, proxmox.VirtualMachineOption{Name: data.Device, Value: want})
		if strings.HasPrefix(current, storage.Name+":iso/"+ciISOPrefix(vmID)) {
			replaced, _, _ = strings.Cut(current, ",")
		}
	}
	if len(drop) > 0 {
		sort.Strings(drop)
		options = append(options, proxmox.VirtualMachineOption{Name: "delete", Value: strings.Join(drop, ",")})
	}
	if len(options) > 0 {
		task, cerr := vm.Config(ctx, options...)
		if cerr != nil {
			return "", fmt.Errorf("attaching %s: %w", volid, cerr)
		}
		if err = task.WaitFor(ctx, ciTaskTimeout); err != nil {
			return "", fmt.Errorf("attaching %s: %w", volid, err)
		}
	}

	// Superseded ISOs are no longer attached. A failed delete is retried on the
	// next reconcile, so it does not fail this one.
	for _, old := range ours {
		if old != volid {
			if derr := deleteVolume(ctx, storage, old); derr != nil {
				log.Log.Error(derr, "Failed to delete superseded cloud-init ISO", "volume", old)
			}
		}
	}
	return replaced, nil
}

// CloudInitISOs lists the cloud-init ISOs this operator uploaded for a VM. The
// deletion path calls it before deleting the VM, while the VM ID still resolves.
func (pc *ProxmoxClient) CloudInitISOs(ctx context.Context, vmName, nodeName,
	storageName string) ([]string, error) {
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return nil, err
	}
	vmID, err := pc.getVMID(vmName, nodeName)
	if err != nil {
		return nil, err
	}
	storage, err := ciStorage(ctx, node, storageName)
	if err != nil {
		return nil, err
	}
	return ciISOs(ctx, storage, vmID)
}

// DeleteCloudInitISOs deletes volumes returned by CloudInitISOs.
func (pc *ProxmoxClient) DeleteCloudInitISOs(ctx context.Context, nodeName, storageName string,
	volids []string) error {
	if len(volids) == 0 {
		return nil
	}
	node, err := pc.getNode(ctx, nodeName)
	if err != nil {
		return err
	}
	storage, err := ciStorage(ctx, node, storageName)
	if err != nil {
		return err
	}
	var errs []error
	for _, volid := range volids {
		errs = append(errs, deleteVolume(ctx, storage, volid))
	}
	return errors.Join(errs...)
}

// ciStorage returns the named storage, or the node's first storage that
// accepts iso content.
func ciStorage(ctx context.Context, node *proxmox.Node, name string) (*proxmox.Storage, error) {
	if name != "" {
		return node.Storage(ctx, name)
	}
	return node.StorageISO(ctx)
}

// ciISOs lists the volids of this VM's cloud-init ISOs on storage.
func ciISOs(ctx context.Context, storage *proxmox.Storage, vmID int) ([]string, error) {
	content, err := storage.GetContent(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing %s content: %w", storage.Name, err)
	}
	prefix := storage.Name + ":iso/" + ciISOPrefix(vmID)
	var volids []string
	for _, c := range content {
		if strings.HasPrefix(c.Volid, prefix) {
			volids = append(volids, c.Volid)
		}
	}
	return volids, nil
}

func deleteVolume(ctx context.Context, storage *proxmox.Storage, volid string) error {
	task, err := storage.DeleteContent(ctx, volid)
	if err != nil {
		return fmt.Errorf("deleting %s: %w", volid, err)
	}
	if err = task.WaitFor(ctx, ciTaskTimeout); err != nil {
		return fmt.Errorf("deleting %s: %w", volid, err)
	}
	return nil
}
