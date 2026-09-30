package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/adapters"
	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
)

type DemoDocument struct {
	Kind      string                     `json:"kind"`
	Device    adapters.InventoryDocument `json:"device_inventory"`
	Workspace adapters.InventoryDocument `json:"workspace_inventory"`
	Renamed   adapters.MutationDocument  `json:"renamed_fixture"`
	Effects   string                     `json:"effects"`
}

func runDemo(ctx context.Context, args []string, stdout io.Writer) error {
	set := newFlagSet("demo")
	ownerBinary := set.String("owner-binary", "", "architecture-poc binary")
	if err := parseFlags(set, args); err != nil {
		return err
	}
	if *ownerBinary == "" {
		return usage("demo requires --owner-binary")
	}
	root, err := os.MkdirTemp("", "architecture-workspace-poc-demo-*")
	if err != nil {
		return application.NewError(application.CodeOperationFailed, "create demo workspace", err)
	}
	defer os.RemoveAll(root)
	localProfile := filepath.Join(root, "local")
	remoteProfile := filepath.Join(root, "remote")
	localOwner, err := startDemoOwner(ctx, *ownerBinary, localProfile)
	if err != nil {
		return err
	}
	defer localOwner.stop()
	remoteOwner, err := startDemoOwner(ctx, *ownerBinary, remoteProfile)
	if err != nil {
		return err
	}
	defer remoteOwner.stop()

	workspace := openWorkspace(filepath.Join(root, "workspace.json"), *ownerBinary, localProfile)
	deviceClient := workspace.DeviceClient()
	workspaceClient := workspace.WorkspaceClient()
	deviceBefore, err := deviceClient.Inventory(ctx)
	if err != nil {
		return err
	}
	localTarget := deviceBefore.Targets[0]
	if _, err := deviceClient.CreateFixture(ctx, localTarget.Target, localTarget.Token, demoFixture("same", "Local")); err != nil {
		return err
	}
	if _, err := workspace.Connect(ctx, application.NewConnection{
		ID: "remote", Label: "Remote", Endpoint: application.Endpoint{Profile: remoteProfile, Access: application.Access{Kind: application.AccessLocal}},
	}); err != nil {
		return err
	}
	remoteEmpty, err := workspace.RefreshConnection(ctx, "remote")
	if err != nil {
		return err
	}
	if _, err := workspaceClient.CreateFixture(ctx, remoteEmpty.Target, remoteEmpty.Token, demoFixture("same", "Remote")); err != nil {
		return err
	}
	remotePopulated, err := workspace.RefreshConnection(ctx, "remote")
	if err != nil {
		return err
	}
	if _, err := workspace.RenameConnection(ctx, "remote", "Remote renamed"); err != nil {
		return err
	}
	if len(remotePopulated.Sessions) != 1 {
		return application.NewError(application.CodeOperationFailed, "demo remote fixture did not appear", nil)
	}
	row := remotePopulated.Sessions[0]
	renamed, err := workspaceClient.RenameFixture(ctx, row.Target, row.Token, "Remote Renamed")
	if err != nil {
		return err
	}
	if _, err := workspace.RefreshConnection(ctx, "remote"); err != nil {
		return err
	}
	deviceInventory, err := deviceClient.Inventory(ctx)
	if err != nil {
		return err
	}
	workspaceInventory, err := workspaceClient.Inventory(ctx)
	if err != nil {
		return err
	}
	deviceDocument, err := adapters.PresentInventory(deviceInventory)
	if err != nil {
		return err
	}
	workspaceDocument, err := adapters.PresentInventory(workspaceInventory)
	if err != nil {
		return err
	}
	renameDocument, err := adapters.PresentMutation(renamed)
	if err != nil {
		return err
	}
	if err := remoteOwner.stop(); err != nil {
		return err
	}
	if err := localOwner.stop(); err != nil {
		return err
	}
	return writeJSON(stdout, DemoDocument{
		Kind: "architecture_workspace_poc_demo", Device: deviceDocument, Workspace: workspaceDocument,
		Renamed: renameDocument, Effects: "inventory-only fixtures; no agent or tmux process launched",
	})
}

func demoFixture(id, name string) application.FixtureDraft {
	return application.FixtureDraft{ID: id, Name: name, Tool: "codex", Cwd: "/tmp/architecture-workspace-poc-" + id, Group: "core"}
}

type demoOwnerProcess struct {
	command *exec.Cmd
	log     bytes.Buffer
	done    chan error
	once    sync.Once
	stopErr error
}

func startDemoOwner(ctx context.Context, binary, profile string) (*demoOwnerProcess, error) {
	if err := os.MkdirAll(profile, 0o700); err != nil {
		return nil, application.NewError(application.CodeOperationFailed, "create demo owner profile", err)
	}
	owner := &demoOwnerProcess{done: make(chan error, 1)}
	owner.command = exec.Command(binary, "serve", "--dir", profile, "--revision", "3")
	owner.command.Stdout = &owner.log
	owner.command.Stderr = &owner.log
	if err := owner.command.Start(); err != nil {
		return nil, application.NewError(application.CodeOffline, "start demo owner", err)
	}
	go func() { owner.done <- owner.command.Wait() }()
	bridge := adapters.NewOwnerBridge(adapters.LocalCommandRunner{}, binary, profile)
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := bridge.Inspect(ctx); err == nil {
			return owner, nil
		}
		select {
		case <-ctx.Done():
			_ = owner.stop()
			return nil, application.NewError(application.CodeOperationFailed, "wait for demo owner", ctx.Err())
		case <-deadline.C:
			_ = owner.stop()
			return nil, application.NewError(application.CodeOffline, "demo owner did not become ready: "+owner.log.String(), nil)
		case <-ticker.C:
		}
	}
}

func (owner *demoOwnerProcess) stop() error {
	owner.once.Do(func() {
		if err := owner.command.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
			owner.stopErr = application.NewError(application.CodeOperationFailed, "stop demo owner", err)
			return
		}
		select {
		case err := <-owner.done:
			if err != nil {
				owner.stopErr = application.NewError(application.CodeOperationFailed, "wait for demo owner shutdown: "+owner.log.String(), err)
			}
		case <-time.After(3 * time.Second):
			_ = owner.command.Process.Kill()
			<-owner.done
			owner.stopErr = application.NewError(application.CodeOperationFailed, "demo owner did not stop after interrupt", nil)
		}
	})
	return owner.stopErr
}
