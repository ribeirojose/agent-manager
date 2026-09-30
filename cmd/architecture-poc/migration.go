package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/adapters"
	"github.com/YoanWai/agent-manager/internal/cli"
	"github.com/YoanWai/agent-manager/internal/config"
	"github.com/YoanWai/agent-manager/internal/hooks"
	"github.com/YoanWai/agent-manager/internal/mcpserver"
	"github.com/YoanWai/agent-manager/internal/sessioncmd"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/tmux"
	"github.com/YoanWai/agent-manager/internal/ui"
	tea "github.com/charmbracelet/bubbletea"
)

const (
	operationArchiveSession  = "session.archive"
	operationMaintainInbox   = "inbox.maintain"
	capabilityArchiveSession = "session.archive.bound"
	capabilityMaintainInbox  = "inbox.maintain.bound"
)

type archiveArguments struct {
	CommandGuards
	CallerID  string `json:"caller_id"`
	SessionID string `json:"session_id"`
	Archived  bool   `json:"archived"`
	Frontend  string `json:"frontend"`
}

type archiveResult struct {
	Kind    string             `json:"kind"`
	Session sessioncmd.Session `json:"session"`
}

func runMigrationOwner(args []string, log io.Writer) error {
	set := flag.NewFlagSet("serve-migration", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	dir := set.String("dir", "", "disposable profile directory")
	socket := set.String("tmux-socket", "", "isolated POC tmux socket")
	if err := set.Parse(args); err != nil {
		return err
	}
	if set.NArg() != 0 || *dir == "" || !strings.HasPrefix(*socket, "am-poc-") || strings.ContainsAny(*socket, "/\\") {
		return errors.New("usage: architecture-poc serve-migration --dir PATH --tmux-socket am-poc-NAME; use a disposable profile")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveOwner(ctx, *dir, 4, log, func(owner *ownerState) error {
		cfg, err := config.LoadDir(*dir)
		if err != nil {
			return err
		}
		driver, err := tmux.NewWithSocket(*socket)
		if err != nil {
			return err
		}
		owner.sessionOwner = sessioncmd.NewSessionOwner(cfg, owner.store, driver)
		owner.inboxOwner = sessioncmd.NewInboxOwner(owner.store)
		owner.migrationLog = log
		owner.info.Capabilities = append(owner.info.Capabilities, capabilityArchiveSession, capabilityMaintainInbox)
		return nil
	})
}

func (owner *ownerState) dispatchMigration(request Request) Response {
	capability := capabilityArchiveSession
	if request.Operation == operationMaintainInbox {
		capability = capabilityMaintainInbox
	}
	if !owner.hasCapability(capability) {
		return owner.reject(request.RequestID, errorMissingCapability, "owner does not support "+capability)
	}
	if request.Operation == operationMaintainInbox {
		var guard CommandGuards
		if err := decodeArguments(request.Arguments, &guard); err != nil {
			return owner.reject(request.RequestID, errorInvalidRequest, err.Error())
		}
		if code, message := owner.guardError(guard); code != "" {
			return owner.reject(request.RequestID, code, message)
		}
		if err := owner.inboxOwner.MaintainInbox(); err != nil {
			return owner.reject(request.RequestID, errorOperationFailed, err.Error())
		}
		fmt.Fprintln(owner.migrationLog, "MIGRATION", request.Operation)
		return owner.succeed(request.RequestID, struct {
			Kind string `json:"kind"`
		}{request.Operation})
	}
	var args archiveArguments
	if err := decodeArguments(request.Arguments, &args); err != nil {
		return owner.reject(request.RequestID, errorInvalidRequest, err.Error())
	}
	if code, message := owner.guardError(args.CommandGuards); code != "" {
		return owner.reject(request.RequestID, code, message)
	}
	var words sessioncmd.Vocabulary
	switch args.Frontend {
	case "cli":
		words = sessioncmd.CLIVocabulary()
	case "mcp":
		words = sessioncmd.MCPVocabulary()
	default:
		return owner.reject(request.RequestID, errorInvalidRequest, "frontend must be cli or mcp")
	}
	session, err := owner.sessionOwner.Archive(sessioncmd.ArchiveRequest{CallerID: args.CallerID, TargetID: args.SessionID, Archived: args.Archived, Words: words})
	if err != nil {
		return owner.reject(request.RequestID, errorOperationFailed, err.Error())
	}
	fmt.Fprintln(owner.migrationLog, "MIGRATION", request.Operation)
	return owner.succeed(request.RequestID, archiveResult{Kind: request.Operation, Session: session})
}

func runMigrationClient(mode string, args []string) error {
	set := flag.NewFlagSet(mode, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	dir := set.String("dir", "", "disposable profile directory")
	caller := set.String("caller", "", "calling session id")
	socket := set.String("tmux-socket", "", "isolated POC tmux socket")
	if err := set.Parse(args); err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("migration client requires --dir PATH")
	}
	if mode == "migration-cli" && (set.NArg() == 0 || set.Arg(0) != "archive") {
		return errors.New("migration-cli routes archive only; use -- archive ID [--restore] [--json]")
	}
	if mode != "migration-cli" && set.NArg() != 0 {
		return errors.New("unexpected migration client arguments")
	}
	if mode == "migration-tui" && (!strings.HasPrefix(*socket, "am-poc-") || strings.ContainsAny(*socket, "/\\")) {
		return errors.New("migration-tui requires --tmux-socket am-poc-NAME")
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), bridgeTimeout)
	defer cancel()
	owner, err := adapters.NewMigrationOwner(ctx, adapters.LocalCommandRunner{}, binary, *dir)
	if err != nil {
		return err
	}
	switch mode {
	case "migration-cli":
		return cli.CommandsWithArchiveOwner("architecture-migration", owner)["archive"](set.Args()[1:], func() string { return *caller }, *dir)
	case "migration-mcp":
		return mcpserver.RunWithArchiveOwner(*dir, *caller, "architecture-migration", owner)
	}
	cfg, err := config.LoadDir(*dir)
	if err != nil {
		return err
	}
	state, err := store.Open(filepath.Join(*dir, "state.db"))
	if err != nil {
		return err
	}
	defer state.Close()
	driver, err := tmux.NewWithSocket(*socket)
	if err != nil {
		return err
	}
	engine, err := status.NewEngine(cfg)
	if err != nil {
		return err
	}
	model := ui.NewWithInboxOwner(cfg, state, driver, engine, hooks.NewManager(*dir), "architecture-migration", owner)
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion(), tea.WithOutput(model.CursorOutput(os.Stdout)))
	model.StartPoller(program.Send)
	_, err = program.Run()
	return err
}
