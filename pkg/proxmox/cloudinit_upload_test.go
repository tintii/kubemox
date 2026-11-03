package proxmox

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/luthermonson/go-proxmox"
)

const uploadUPID = "UPID:pve0:00000001:00000001:00000001:imgcopy:local:root@pam:"

// fakeUploadStorage accepts uploads the way pveproxy does: the first form part
// named "filename" must be the file, and its filename is the stored name.
type fakeUploadStorage struct {
	stored  string
	content string
}

func (f *fakeUploadStorage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api2/json/nodes/pve0/storage/local/status":
		_, _ = w.Write([]byte(`{"data":{"type":"dir","active":1}}`))
	case "/api2/json/nodes/pve0/storage/local/upload":
		reader, err := r.MultipartReader()
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil || part.FormName() != "filename" {
				continue
			}
			if part.FileName() == "" {
				// pveproxy: "wrong field name for file upload", empty body.
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			body, _ := io.ReadAll(part)
			f.stored, f.content = part.FileName(), string(body)
			break
		}
		_, _ = w.Write([]byte(`{"data":"` + uploadUPID + `"}`))
	default:
		// Task status. Real Proxmox echoes upid and node; go-proxmox's Task
		// decoder clears the task ID when they are missing.
		_, _ = w.Write([]byte(`{"data":{"upid":"` + uploadUPID + `","node":"pve0",` +
			`"status":"stopped","exitstatus":"OK"}}`))
	}
}

func TestUploadCloudInitISOStoresUnderTheGivenName(t *testing.T) {
	fake := &fakeUploadStorage{}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	client := proxmox.NewClient(server.URL + "/api2/json")
	node := (&proxmox.Node{}).New(client, "pve0")
	storage, err := node.Storage(context.Background(), "local")
	if err != nil {
		t.Fatalf("storage: %v", err)
	}

	dir := t.TempDir()
	isoPath := filepath.Join(dir, "cidata.iso")
	if err := os.WriteFile(isoPath, []byte("iso bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	const name = "kubemox-ci-102-98949f70a9a6.iso"
	if err := uploadCloudInitISO(context.Background(), storage, isoPath, name); err != nil {
		t.Fatalf("uploadCloudInitISO: %v", err)
	}
	if fake.stored != name || fake.content != "iso bytes" {
		t.Errorf("stored %q with %q, want %q with the ISO bytes", fake.stored, fake.content, name)
	}
}
