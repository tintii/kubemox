package proxmox

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	proxmox "github.com/luthermonson/go-proxmox"
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

// fakeTemplateVM serves the Proxmox API calls a template build makes for one
// VM, and records the disk resizes it is asked for.
type fakeTemplateVM struct {
	mu      sync.Mutex
	scsi0   string
	resizes []string
}

func (f *fakeTemplateVM) setSCSI0(v string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scsi0 = v
}

func (f *fakeTemplateVM) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	const vm = "/api2/json/nodes/pve0/qemu/9000"
	switch path := r.URL.Path; {
	case path == "/api2/json/nodes/pve0/status":
		_, _ = w.Write([]byte(`{"data":{}}`))
	case path == "/api2/json/nodes/pve0/qemu":
		_, _ = w.Write([]byte(`{"data":[{"vmid":9000,"name":"fedora44-k3s"}]}`))
	case path == vm+"/status/current":
		_, _ = w.Write([]byte(`{"data":{"vmid":9000,"name":"fedora44-k3s"}}`))
	case path == vm+"/config":
		_, _ = fmt.Fprintf(w, `{"data":{"scsi0":%q}}`, f.scsi0)
	case path == vm+"/resize" && r.Method == http.MethodPut:
		var body struct{ Disk, Size string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.resizes = append(f.resizes, body.Disk+"="+body.Size)
		_, _ = w.Write([]byte(`{"data":"UPID:pve0:00000001:00000001:00000001:qmresize:9000:root@pam:"}`))
	case strings.HasPrefix(path, "/api2/json/nodes/pve0/tasks/"):
		_, _ = w.Write([]byte(`{"data":{"status":"stopped","exitstatus":"OK"}}`))
	default:
		http.NotFound(w, r)
	}
}

func newFakeTemplateClient(t *testing.T, fake *fakeTemplateVM) *ProxmoxClient {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return &ProxmoxClient{
		Client:     proxmox.NewClient(server.URL + "/api2/json"),
		nodesCache: make(map[string]NodeCache),
	}
}

// The VM is cached before the disk import, as ImportDiskToVM used to do. The
// resize must still see the imported scsi0 and grow it.
func TestResizeTemplateDiskIgnoresVMCachedBeforeImport(t *testing.T) {
	fake := &fakeTemplateVM{}
	pc := newFakeTemplateClient(t, fake)
	if _, err := pc.getVirtualMachine("fedora44-k3s", "pve0"); err != nil {
		t.Fatalf("caching VM: %v", err)
	}
	fake.setSCSI0("local-lvm:vm-9000-disk-0,size=5G")

	if err := pc.ResizeTemplateDisk("fedora44-k3s", "pve0", 40); err != nil {
		t.Fatalf("ResizeTemplateDisk: %v", err)
	}
	if want := []string{"scsi0=40G"}; fmt.Sprint(fake.resizes) != fmt.Sprint(want) {
		t.Errorf("resizes = %v, want %v", fake.resizes, want)
	}
	cached, err := pc.getVirtualMachine("fedora44-k3s", "pve0")
	if err != nil {
		t.Fatalf("reading cached VM: %v", err)
	}
	if cached.VirtualMachineConfig.SCSI0 == "" {
		t.Error("cache still holds the VM from before the import")
	}
}

func TestResizeTemplateDiskFailsWithoutImportedDisk(t *testing.T) {
	fake := &fakeTemplateVM{}
	pc := newFakeTemplateClient(t, fake)
	if err := pc.ResizeTemplateDisk("fedora44-k3s", "pve0", 40); err == nil {
		t.Fatal("ResizeTemplateDisk succeeded with no scsi0")
	}
	if err := pc.ResizeTemplateDisk("fedora44-k3s", "pve0", 0); err != nil {
		t.Errorf("ResizeTemplateDisk without diskSize: %v", err)
	}
	if len(fake.resizes) != 0 {
		t.Errorf("resizes = %v, want none", fake.resizes)
	}
}
