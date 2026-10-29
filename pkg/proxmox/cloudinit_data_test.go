package proxmox

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
	"github.com/alperencelik/kubemox/pkg/kubernetes"
)

const (
	ciNamespace = "homelab"
	ciVMName    = "k00w00"
	ciSecret    = "k00w00-cloudinit"
	ciConfigMap = "node-prep"
	// ciSecretValue doubles as a canary: no error message may contain it.
	ciSecretValue = "#cloud-config\nk3s-token: do-not-leak-this-value\n" //nolint:gosec // test fixture
	ciVendorValue = "#cloud-config\nruncmd: [swapoff -a]\n"
)

func cloudInitVM(data *proxmoxv1alpha1.CloudInitData) *proxmoxv1alpha1.VirtualMachine {
	return &proxmoxv1alpha1.VirtualMachine{
		ObjectMeta: metav1.ObjectMeta{Name: ciVMName}, // cluster-scoped
		Spec:       proxmoxv1alpha1.VirtualMachineSpec{Name: ciVMName, CloudInitData: data},
	}
}

func secretRef(name, key string) proxmoxv1alpha1.CloudInitDataSource {
	return proxmoxv1alpha1.CloudInitDataSource{SecretKeyRef: &corev1.SecretKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key}}
}

func configMapRef(name, key string) *proxmoxv1alpha1.CloudInitDataSource {
	return &proxmoxv1alpha1.CloudInitDataSource{ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
		LocalObjectReference: corev1.LocalObjectReference{Name: name}, Key: key}}
}

func cloudInitObjects() (*corev1.Secret, *corev1.ConfigMap) {
	return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: ciSecret, Namespace: ciNamespace},
			Data:       map[string][]byte{"user-data": []byte(ciSecretValue)},
		}, &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: ciConfigMap, Namespace: ciNamespace},
			Data:       map[string]string{"vendor-data": ciVendorValue},
		}
}

func TestResolveCloudInitData(t *testing.T) {
	secret, cm := cloudInitObjects()
	c := credentialsClient(t, secret, cm)
	vm := cloudInitVM(&proxmoxv1alpha1.CloudInitData{
		UserData:   secretRef(ciSecret, "user-data"),
		VendorData: configMapRef(ciConfigMap, "vendor-data"),
	})

	files, err := ResolveCloudInitData(context.Background(), c, ciNamespace, vm)
	if err != nil {
		t.Fatalf("ResolveCloudInitData: %v", err)
	}
	want := map[string]string{
		ciUserData:   ciSecretValue,
		ciVendorData: ciVendorValue,
		ciMetaData:   "instance-id: k00w00\nlocal-hostname: k00w00\n",
	}
	if len(files) != len(want) {
		t.Fatalf("files = %v, want keys %v", files, want)
	}
	for name, value := range want {
		if files[name] != value {
			t.Errorf("%s = %q, want %q", name, files[name], value)
		}
	}
}

func TestResolveCloudInitDataExplicitMetaData(t *testing.T) {
	secret, cm := cloudInitObjects()
	cm.Data["meta-data"] = "instance-id: custom\n"
	c := credentialsClient(t, secret, cm)
	vm := cloudInitVM(&proxmoxv1alpha1.CloudInitData{
		UserData: secretRef(ciSecret, "user-data"),
		MetaData: configMapRef(ciConfigMap, "meta-data"),
	})

	files, err := ResolveCloudInitData(context.Background(), c, ciNamespace, vm)
	if err != nil {
		t.Fatalf("ResolveCloudInitData: %v", err)
	}
	if got := files[ciMetaData]; got != "instance-id: custom\n" {
		t.Errorf("meta-data = %q, want the referenced value", got)
	}
}

func TestResolveCloudInitDataMissing(t *testing.T) {
	secret, cm := cloudInitObjects()
	tests := []struct {
		name    string
		data    *proxmoxv1alpha1.CloudInitData
		wantKey string
	}{
		{"missing secret", &proxmoxv1alpha1.CloudInitData{UserData: secretRef("absent", "user-data")}, ""},
		{"missing secret key", &proxmoxv1alpha1.CloudInitData{UserData: secretRef(ciSecret, "absent")}, "absent"},
		{"missing configmap", &proxmoxv1alpha1.CloudInitData{
			UserData: secretRef(ciSecret, "user-data"), VendorData: configMapRef("absent", "vendor-data")}, ""},
		{"missing configmap key", &proxmoxv1alpha1.CloudInitData{
			UserData: secretRef(ciSecret, "user-data"), VendorData: configMapRef(ciConfigMap, "absent")}, "absent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := credentialsClient(t, secret.DeepCopy(), cm.DeepCopy())
			_, err := ResolveCloudInitData(context.Background(), c, ciNamespace, cloudInitVM(tt.data))
			var missing *kubernetes.MissingReferenceError
			if !errors.As(err, &missing) {
				t.Fatalf("err = %v, want MissingReferenceError", err)
			}
			if missing.Key != tt.wantKey {
				t.Errorf("missing key = %q, want %q", missing.Key, tt.wantKey)
			}
			if strings.Contains(err.Error(), "do-not-leak-this-value") {
				t.Errorf("error leaks the secret value: %v", err)
			}
		})
	}
}

func TestResolveCloudInitDataOptionalMissing(t *testing.T) {
	secret, _ := cloudInitObjects()
	optional := true
	vendor := configMapRef("absent", "vendor-data")
	vendor.ConfigMapKeyRef.Optional = &optional
	c := credentialsClient(t, secret)
	vm := cloudInitVM(&proxmoxv1alpha1.CloudInitData{UserData: secretRef(ciSecret, "user-data"), VendorData: vendor})

	files, err := ResolveCloudInitData(context.Background(), c, ciNamespace, vm)
	if err != nil {
		t.Fatalf("ResolveCloudInitData: %v", err)
	}
	if got, ok := files[ciVendorData]; !ok || got != "" {
		t.Errorf("vendor-data = %q (present %v), want empty", got, ok)
	}
}

// readISO returns the label and files of a NoCloud ISO built by BuildNoCloudISO.
func readISO(t *testing.T, isoPath string, names ...string) (string, map[string]string) {
	t.Helper()
	backend, err := file.OpenFromPath(isoPath, true)
	if err != nil {
		t.Fatalf("open ISO: %v", err)
	}
	defer func() { _ = backend.Close() }()
	fs, err := iso9660.Read(backend, 0, 0, ciBlockSize)
	if err != nil {
		t.Fatalf("read ISO: %v", err)
	}
	got := map[string]string{}
	for _, name := range names {
		f, err := fs.OpenFile("/"+name, os.O_RDONLY)
		if err != nil {
			continue
		}
		content, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		got[name] = string(content)
	}
	// The volume identifier field is fixed-width; go-diskfs pads it with NULs.
	return strings.TrimRight(fs.Label(), "\x00 "), got
}

func TestBuildNoCloudISO(t *testing.T) {
	files := map[string]string{
		ciUserData:      ciSecretValue,
		ciMetaData:      "instance-id: k00w00\n",
		ciVendorData:    ciVendorValue,
		ciNetworkConfig: "", // empty documents are left out
	}
	isoPath, hash, err := BuildNoCloudISO(files)
	if err != nil {
		t.Fatalf("BuildNoCloudISO: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(isoPath)) })

	label, got := readISO(t, isoPath, ciUserData, ciMetaData, ciVendorData, ciNetworkConfig)
	if label != ciVolumeLabel {
		t.Errorf("label = %q, want %q", label, ciVolumeLabel)
	}
	for _, name := range []string{ciUserData, ciMetaData, ciVendorData} {
		if got[name] != files[name] {
			t.Errorf("%s = %q, want %q", name, got[name], files[name])
		}
	}
	if _, ok := got[ciNetworkConfig]; ok {
		t.Errorf("empty network-config was written")
	}

	isoPath2, hash2, err := BuildNoCloudISO(files)
	if err != nil {
		t.Fatalf("BuildNoCloudISO again: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(isoPath2)) })
	if hash != hash2 {
		t.Errorf("identical input gave hashes %s and %s", hash, hash2)
	}

	delete(files, ciNetworkConfig)
	isoPath3, hash3, err := BuildNoCloudISO(files)
	if err != nil {
		t.Fatalf("BuildNoCloudISO without empty doc: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(isoPath3)) })
	if hash3 != hash {
		t.Errorf("dropping an empty document changed the hash")
	}

	files[ciUserData] += "# changed\n"
	isoPath4, hash4, err := BuildNoCloudISO(files)
	if err != nil {
		t.Fatalf("BuildNoCloudISO changed: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(isoPath4)) })
	if hash4 == hash {
		t.Errorf("changed user-data kept the same hash")
	}
}

func TestPVECloudInitVolume(t *testing.T) {
	for value, want := range map[string]bool{
		"local-lvm:vm-100-cloudinit,media=cdrom":            true,
		"local:100/vm-100-cloudinit.qcow2,media=cdrom":      true,
		"local:iso/kubemox-ci-100-0123abcd.iso,media=cdrom": false,
		"local-lvm:vm-100-disk-0,size=40G":                  false,
		"none,media=cdrom":                                  false,
	} {
		if got := pveCloudInitVolume.MatchString(value); got != want {
			t.Errorf("%q matched = %v, want %v", value, got, want)
		}
	}
}
