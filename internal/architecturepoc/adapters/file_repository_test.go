package adapters

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
)

func TestFileRepositoryPersistsAdapterDTOAndSSHRoute(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	repository := NewFileRepository(path, "device-a")
	ctx := context.Background()
	connection := application.Connection{
		ID: "lab", Label: "Lab", Generation: 7, SavedEnvironmentID: "env-1",
		Endpoint: application.Endpoint{Profile: "/srv/profile", Access: application.Access{Kind: application.AccessSSH, Host: "demo@example", OwnerBinary: "/opt/architecture-poc"}},
	}
	if err := repository.Update(ctx, func(state *application.WorkspaceState) error {
		state.NextGeneration = 8
		state.Connections = append(state.Connections, connection)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(contents)
	for _, fragment := range []string{`"version": 1`, `"device_id": "device-a"`, `"kind": "ssh"`, `"host": "demo@example"`, `"owner_binary": "/opt/architecture-poc"`} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("config missing %s:\n%s", fragment, text)
		}
	}
	if strings.Contains(text, "SavedEnvironmentID") || strings.Contains(text, "Endpoint") {
		t.Fatalf("domain structs leaked into JSON: %s", text)
	}

	reopened := NewFileRepository(path, "ignored-after-create")
	if err := reopened.View(ctx, func(state application.WorkspaceState) error {
		if state.DeviceID != "device-a" || state.NextGeneration != 8 || len(state.Connections) != 1 || state.Connections[0].Endpoint != connection.Endpoint {
			t.Fatalf("reopened state = %+v", state)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFileRepositorySerializesIndependentInstancesWithoutLostUpdates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	first := NewFileRepository(path, "local")
	second := NewFileRepository(path, "local")
	ctx := context.Background()
	entered := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- first.Update(ctx, func(state *application.WorkspaceState) error {
			close(entered)
			<-release
			state.Connections = append(state.Connections, application.Connection{ID: "first", Label: "First", Generation: 1})
			return nil
		})
	}()
	<-entered
	secondDone := make(chan error, 1)
	go func() {
		secondDone <- second.Update(ctx, func(state *application.WorkspaceState) error {
			state.Connections = append(state.Connections, application.Connection{ID: "second", Label: "Second", Generation: 2})
			return nil
		})
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("second transaction bypassed lock: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if err := first.View(ctx, func(state application.WorkspaceState) error {
		if len(state.Connections) != 2 {
			t.Fatalf("lost update: %+v", state.Connections)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFileRepositoryRejectsUnknownSchemaAndDoesNotOverwriteIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	original := []byte(`{"version":2,"device_id":"future","connections":[]}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	repository := NewFileRepository(path, "local")
	err := repository.Update(context.Background(), func(state *application.WorkspaceState) error { return nil })
	if application.CodeOf(err) != application.CodeUnsupportedProtocol {
		t.Fatalf("schema error = %v", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != string(original) {
		t.Fatalf("future config overwritten: %s", after)
	}
}

func TestConfigIsOneJSONDocument(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	repository := NewFileRepository(path, "local")
	if err := repository.Update(context.Background(), func(*application.WorkspaceState) error { return nil }); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	var first map[string]any
	if err := decoder.Decode(&first); err != nil {
		t.Fatal(err)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		t.Fatalf("config contains a second JSON value: %#v", extra)
	}
}
