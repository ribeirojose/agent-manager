package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/adapters"
	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
	"github.com/YoanWai/agent-manager/internal/architecturepoc/extensions"
)

func runCLI(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		return usage("usage: architecture-workspace-poc <connect|refresh|inspect|rename-connection|retarget|reconnect|remove-connection|fixture-create|fixture-rename|mcp|demo>")
	}
	switch args[0] {
	case "connect":
		return runConnect(ctx, args[1:], stdout)
	case "refresh":
		return runRefresh(ctx, args[1:], stdout)
	case "inspect":
		return runInspect(ctx, args[1:], stdout)
	case "rename-connection":
		return runRenameConnection(ctx, args[1:], stdout)
	case "retarget":
		return runRetarget(ctx, args[1:], stdout)
	case "reconnect":
		return runReconnect(ctx, args[1:], stdout)
	case "remove-connection":
		return runRemoveConnection(ctx, args[1:], stdout)
	case "fixture-create":
		return runFixtureCreate(ctx, args[1:], stdout)
	case "fixture-rename":
		return runFixtureRename(ctx, args[1:], stdout)
	case "mcp":
		return runMCP(ctx, args[1:], stdin, stdout)
	case "demo":
		return runDemo(ctx, args[1:], stdout)
	default:
		return usage(fmt.Sprintf("unknown command %q", args[0]))
	}
}

func runConnect(ctx context.Context, args []string, stdout io.Writer) error {
	set := newFlagSet("connect")
	config := set.String("config", "", "workspace config")
	ownerBinary := set.String("owner-binary", "", "local owner binary")
	id := set.String("id", "", "stable connection id")
	label := set.String("label", "", "display label")
	profile := set.String("profile", "", "owner profile")
	sshHost := set.String("ssh-host", "", "SSH destination")
	remoteBinary := set.String("remote-owner-binary", "", "owner binary on SSH destination")
	if err := parseFlags(set, args); err != nil {
		return err
	}
	if *config == "" || *ownerBinary == "" || *id == "" || *label == "" || *profile == "" {
		return usage("connect requires --config, --owner-binary, --id, --label, and --profile")
	}
	endpoint, err := routeEndpoint(*profile, *sshHost, *remoteBinary)
	if err != nil {
		return err
	}
	workspace := openWorkspace(*config, *ownerBinary, "")
	connection, err := workspace.Connect(ctx, application.NewConnection{ID: *id, Label: *label, Endpoint: endpoint})
	if err != nil {
		return err
	}
	return writeJSON(stdout, adapters.PresentConnection(connection))
}

func runRefresh(ctx context.Context, args []string, stdout io.Writer) error {
	set := newFlagSet("refresh")
	config := set.String("config", "", "workspace config")
	ownerBinary := set.String("owner-binary", "", "local owner binary")
	id := set.String("id", "", "connection id")
	if err := parseFlags(set, args); err != nil {
		return err
	}
	if *config == "" || *ownerBinary == "" || *id == "" {
		return usage("refresh requires --config, --owner-binary, and --id")
	}
	target, err := openWorkspace(*config, *ownerBinary, "").RefreshConnection(ctx, *id)
	if err != nil {
		return err
	}
	document, err := adapters.PresentInventory(application.Inventory{Scope: application.ScopeWorkspace, Targets: []application.TargetProjection{target}})
	if err != nil {
		return err
	}
	return writeJSON(stdout, document.Targets[0])
}

func runInspect(ctx context.Context, args []string, stdout io.Writer) error {
	set := newFlagSet("inspect")
	config, ownerBinary, localProfile, scopeName := clientFlags(set)
	group := set.String("group", "", "exact group filter")
	if err := parseFlags(set, args); err != nil {
		return err
	}
	client, err := scopedClient(*config, *ownerBinary, *localProfile, *scopeName)
	if err != nil {
		return err
	}
	inventory, err := extensions.NewFilteredInventory(client).Query(ctx, *group)
	if err != nil {
		return err
	}
	document, err := adapters.PresentInventory(inventory)
	if err != nil {
		return err
	}
	return writeJSON(stdout, document)
}

func runRenameConnection(ctx context.Context, args []string, stdout io.Writer) error {
	set := newFlagSet("rename-connection")
	config := set.String("config", "", "workspace config")
	id := set.String("id", "", "connection id")
	label := set.String("label", "", "display label")
	if err := parseFlags(set, args); err != nil {
		return err
	}
	if *config == "" || *id == "" || *label == "" {
		return usage("rename-connection requires --config, --id, and --label")
	}
	connection, err := openWorkspace(*config, "unused", "").RenameConnection(ctx, *id, *label)
	if err != nil {
		return err
	}
	return writeJSON(stdout, adapters.PresentConnection(connection))
}

func runRetarget(ctx context.Context, args []string, stdout io.Writer) error {
	set := newFlagSet("retarget")
	config := set.String("config", "", "workspace config")
	id := set.String("id", "", "connection id")
	profile := set.String("profile", "", "new owner profile")
	sshHost := set.String("ssh-host", "", "SSH destination")
	remoteBinary := set.String("remote-owner-binary", "", "owner binary on SSH destination")
	if err := parseFlags(set, args); err != nil {
		return err
	}
	if *config == "" || *id == "" || *profile == "" {
		return usage("retarget requires --config, --id, and --profile")
	}
	endpoint, err := routeEndpoint(*profile, *sshHost, *remoteBinary)
	if err != nil {
		return err
	}
	connection, err := openWorkspace(*config, "unused", "").RetargetConnection(ctx, *id, endpoint)
	if err != nil {
		return err
	}
	return writeJSON(stdout, adapters.PresentConnection(connection))
}

func runReconnect(ctx context.Context, args []string, stdout io.Writer) error {
	set := newFlagSet("reconnect")
	config := set.String("config", "", "workspace config")
	ownerBinary := set.String("owner-binary", "", "local owner binary")
	id := set.String("id", "", "connection id")
	if err := parseFlags(set, args); err != nil {
		return err
	}
	if *config == "" || *ownerBinary == "" || *id == "" {
		return usage("reconnect requires --config, --owner-binary, and --id")
	}
	connection, err := openWorkspace(*config, *ownerBinary, "").Reconnect(ctx, *id)
	if err != nil {
		return err
	}
	return writeJSON(stdout, adapters.PresentConnection(connection))
}

func runRemoveConnection(ctx context.Context, args []string, stdout io.Writer) error {
	set := newFlagSet("remove-connection")
	config := set.String("config", "", "workspace config")
	id := set.String("id", "", "connection id")
	if err := parseFlags(set, args); err != nil {
		return err
	}
	if *config == "" || *id == "" {
		return usage("remove-connection requires --config and --id")
	}
	if err := openWorkspace(*config, "unused", "").RemoveConnection(ctx, *id); err != nil {
		return err
	}
	return writeJSON(stdout, map[string]string{"removed_connection_id": *id})
}

func runFixtureCreate(ctx context.Context, args []string, stdout io.Writer) error {
	set := newFlagSet("fixture-create")
	config, ownerBinary, localProfile, scopeName := clientFlags(set)
	targetID := set.String("target", "", "device or connection/ID")
	tokenValue := set.String("token", "", "reader token")
	id := set.String("id", "", "fixture id")
	name := set.String("name", "", "fixture name")
	tool := set.String("tool", "", "fixture tool")
	cwd := set.String("cwd", "", "fixture directory")
	group := set.String("group", "", "fixture group")
	if err := parseFlags(set, args); err != nil {
		return err
	}
	if *targetID == "" || *tokenValue == "" || *id == "" || *name == "" || *tool == "" || *cwd == "" || *group == "" {
		return usage("fixture-create requires --target, --token, --id, --name, --tool, --cwd, and --group")
	}
	client, err := scopedClient(*config, *ownerBinary, *localProfile, *scopeName)
	if err != nil {
		return err
	}
	target, err := adapters.DecodeOwnerTarget(*targetID)
	if err != nil {
		return err
	}
	token, err := adapters.DecodeReaderToken(*tokenValue)
	if err != nil {
		return err
	}
	result, err := client.CreateFixture(ctx, target, token, application.FixtureDraft{ID: *id, Name: *name, Tool: *tool, Cwd: *cwd, Group: *group})
	if err != nil {
		return err
	}
	document, err := adapters.PresentMutation(result)
	if err != nil {
		return err
	}
	return writeJSON(stdout, document)
}

func runFixtureRename(ctx context.Context, args []string, stdout io.Writer) error {
	set := newFlagSet("fixture-rename")
	config, ownerBinary, localProfile, scopeName := clientFlags(set)
	targetID := set.String("target", "", "namespaced session target")
	tokenValue := set.String("token", "", "reader token")
	name := set.String("name", "", "new fixture name")
	if err := parseFlags(set, args); err != nil {
		return err
	}
	if *targetID == "" || *tokenValue == "" || *name == "" {
		return usage("fixture-rename requires --target, --token, and --name")
	}
	client, err := scopedClient(*config, *ownerBinary, *localProfile, *scopeName)
	if err != nil {
		return err
	}
	target, err := adapters.DecodeSessionTarget(*targetID)
	if err != nil {
		return err
	}
	token, err := adapters.DecodeReaderToken(*tokenValue)
	if err != nil {
		return err
	}
	result, err := extensions.NewFixtureRename(client).Execute(ctx, extensions.RenameRequest{Target: target, Token: token, Name: *name})
	if err != nil {
		return err
	}
	document, err := adapters.PresentMutation(result)
	if err != nil {
		return err
	}
	return writeJSON(stdout, document)
}

func runMCP(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	set := newFlagSet("mcp")
	config, ownerBinary, localProfile, scopeName := clientFlags(set)
	if err := parseFlags(set, args); err != nil {
		return err
	}
	client, err := scopedClient(*config, *ownerBinary, *localProfile, *scopeName)
	if err != nil {
		return err
	}
	return adapters.RunMCPIO(ctx, client, stdin, stdout)
}

func openWorkspace(config, ownerBinary, localProfile string) *application.Workspace {
	repository := adapters.NewFileRepository(config, "local")
	resolver := adapters.NewProcessOwnerResolver(ownerBinary)
	return application.NewWorkspace(repository, resolver, application.Endpoint{Profile: localProfile, Access: application.Access{Kind: application.AccessLocal}}, nil)
}

func scopedClient(config, ownerBinary, localProfile, scopeName string) (*application.Client, error) {
	if config == "" || ownerBinary == "" || localProfile == "" || scopeName == "" {
		return nil, usage("client command requires --config, --owner-binary, --local-profile, and --scope")
	}
	workspace := openWorkspace(config, ownerBinary, localProfile)
	switch application.Scope(scopeName) {
	case application.ScopeDevice:
		return workspace.DeviceClient(), nil
	case application.ScopeWorkspace:
		return workspace.WorkspaceClient(), nil
	default:
		return nil, usage(fmt.Sprintf("scope must be device or workspace, got %q", scopeName))
	}
}

func clientFlags(set *flag.FlagSet) (config, ownerBinary, localProfile, scope *string) {
	return set.String("config", "", "workspace config"),
		set.String("owner-binary", "", "local owner binary"),
		set.String("local-profile", "", "local owner profile"),
		set.String("scope", "", "device or workspace")
}

func routeEndpoint(profile, sshHost, remoteBinary string) (application.Endpoint, error) {
	profile, sshHost, remoteBinary = strings.TrimSpace(profile), strings.TrimSpace(sshHost), strings.TrimSpace(remoteBinary)
	if sshHost == "" {
		if remoteBinary != "" {
			return application.Endpoint{}, usage("--remote-owner-binary requires --ssh-host")
		}
		return application.Endpoint{Profile: profile, Access: application.Access{Kind: application.AccessLocal}}, nil
	}
	if remoteBinary == "" {
		return application.Endpoint{}, usage("--ssh-host requires --remote-owner-binary")
	}
	return application.Endpoint{Profile: profile, Access: application.Access{Kind: application.AccessSSH, Host: sshHost, OwnerBinary: remoteBinary}}, nil
}

func newFlagSet(name string) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	return set
}

func parseFlags(set *flag.FlagSet, args []string) error {
	if err := set.Parse(args); err != nil {
		return usage(err.Error())
	}
	if set.NArg() != 0 {
		return usage(fmt.Sprintf("unexpected arguments: %s", strings.Join(set.Args(), " ")))
	}
	return nil
}

func usage(message string) error {
	return application.NewError(application.CodeInvalidRequest, message, nil)
}

func writeJSON(writer io.Writer, value any) error {
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		return application.NewError(application.CodeOperationFailed, "write command JSON", err)
	}
	return nil
}
