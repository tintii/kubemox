package proxmox

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	proxmoxv1alpha1 "github.com/alperencelik/kubemox/api/proxmox/v1alpha1"
)

func TestTemplateImportSource(t *testing.T) {
	const file = "Fedora-Cloud-Base-Generic-44-1.7.x86_64.qcow2"
	tests := map[string]struct {
		image proxmoxv1alpha1.StorageDownloadURLContent
		path  string
		want  string
	}{
		"import content uses the volume ID": {
			image: proxmoxv1alpha1.StorageDownloadURLContent{Content: "import", Storage: "local", Filename: file},
			want:  "local:import/" + file,
		},
		"iso content keeps the filesystem path": {
			image: proxmoxv1alpha1.StorageDownloadURLContent{Content: "iso", Storage: "nfs", Filename: "noble.img"},
			path:  "/mnt/pve/nfs",
			want:  "/mnt/pve/nfs/template/iso/noble.img",
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := templateImportSource(tt.image, tt.path); got != tt.want {
				t.Errorf("templateImportSource = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTemplateDiskNeedsResize(t *testing.T) {
	const disk = "local-lvm:vm-9000-disk-0,discard=on,size="
	tests := map[string]struct {
		scsi0 string
		size  int
		want  bool
	}{
		"unset":            {disk + "5G", 0, false},
		"no disk":          {"", 40, false},
		"smaller":          {disk + "5G", 40, true},
		"equal":            {disk + "40G", 40, false},
		"larger":           {disk + "50G", 40, false},
		"megabytes":        {disk + "5120M", 40, true},
		"unparsable":       {"local-lvm:vm-9000-disk-0", 40, false},
		"trailing options": {disk + "40G,ssd=1", 41, true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if got := templateDiskNeedsResize(tt.scsi0, tt.size); got != tt.want {
				t.Errorf("templateDiskNeedsResize(%q, %d) = %v, want %v", tt.scsi0, tt.size, got, tt.want)
			}
		})
	}
}

func TestAdditionalConfigTargetWithoutTypeMeta(t *testing.T) {
	const wantNode = "pve0"
	cfg := map[string]string{"net0": "virtio,bridge=vmbr0,tag=116"}
	// TypeMeta is left empty, as on objects read through the client.
	objs := []Resource{
		&proxmoxv1alpha1.VirtualMachine{Spec: proxmoxv1alpha1.VirtualMachineSpec{
			Name: ciVMName, NodeName: wantNode, AdditionalConfig: cfg}},
		&proxmoxv1alpha1.VirtualMachineTemplate{Spec: proxmoxv1alpha1.VirtualMachineTemplateSpec{
			Name: ciVMName, NodeName: wantNode, AdditionalConfig: cfg}},
	}
	for _, obj := range objs {
		name, node, got, err := additionalConfigTarget(obj)
		if err != nil {
			t.Fatalf("%T: %v", obj, err)
		}
		if name != ciVMName || node != wantNode || got["net0"] != cfg["net0"] {
			t.Errorf("%T: got %q %q %v", obj, name, node, got)
		}
	}
	container := &proxmoxv1alpha1.Container{ObjectMeta: metav1.ObjectMeta{Name: "c"}}
	if _, _, _, err := additionalConfigTarget(container); err == nil {
		t.Error("Container: want an error")
	}
}
