package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/adapters"
	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
	"github.com/YoanWai/agent-manager/internal/sessioncmd"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/tmux"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMigratedProductionCallersUseOwnerProcess(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	profile := t.TempDir()
	binary := filepath.Join(t.TempDir(), "owner")
	if output, err := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build owner: %v\n%s", err, output)
	}
	driver, err := tmux.NewWithSocket("am-poc-" + uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(filepath.Join(profile, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { state.Close() })
	for _, id := range []string{"cafe", "beef"} {
		row := store.Session{ID: id, Name: "migration-" + id, Tool: "codex", Cwd: profile, Status: "idle"}
		if err := driver.Create(id, profile, "printf 'MIGRATION-PANE\\n'; cat", nil, 80, 24); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { driver.Kill(id) })
		row.TmuxSocket = driver.SocketPath()
		if err := state.CreateSession(row); err != nil {
			t.Fatal(err)
		}
	}
	owner := startOwnerProcess(t, profile, exec.Command(binary, "serve-migration", "--dir", profile, "--tmux-socket", driver.SocketName()))
	bound, err := adapters.NewMigrationOwner(t.Context(), adapters.LocalCommandRunner{}, binary, profile)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		pane, err := driver.CapturePane("beef")
		if err == nil && strings.Contains(pane, "MIGRATION-PANE") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("fixture pane did not paint")
		}
		time.Sleep(10 * time.Millisecond)
	}
	output, err := exec.CommandContext(t.Context(), binary, "migration-cli", "--dir", profile, "--caller", "cafe", "--", "archive", "beef", "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("production CLI archive: %v\n%s", err, output)
	}
	var archived sessioncmd.Session
	if err := json.Unmarshal(output, &archived); err != nil {
		t.Fatal(err)
	}
	if !archived.Archived || !archived.Running || !driver.Exists("beef") {
		t.Fatalf("archive = %+v", archived)
	}
	snapshot, err := state.Snapshot("beef")
	if err != nil || !strings.Contains(snapshot, "MIGRATION-PANE") {
		t.Fatalf("snapshot = %q, %v", snapshot, err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "migration-test", Version: "test"}, nil)
	mcpSession, err := client.Connect(t.Context(), &mcp.CommandTransport{Command: exec.Command(binary, "migration-mcp", "--dir", profile, "--caller", "cafe")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mcpSession.Close() })
	result, err := mcpSession.CallTool(t.Context(), &mcp.CallToolParams{Name: "archive_session", Arguments: map[string]any{"session_id": "beef", "archived": false}})
	if err != nil || result.IsError {
		t.Fatalf("production MCP restore: %+v, %v", result, err)
	}
	row, err := state.Get("beef")
	if err != nil || row.Archived || !driver.Exists("beef") {
		t.Fatalf("restored row = %+v, %v", row, err)
	}
	if _, err := bound.Archive(sessioncmd.ArchiveRequest{CallerID: "cafe", TargetID: "cafe", Archived: true, Words: sessioncmd.CLIVocabulary()}); err == nil || !strings.Contains(err.Error(), "archive itself") {
		t.Fatalf("self-archive: %v", err)
	}
	if _, err := bound.Archive(sessioncmd.ArchiveRequest{CallerID: "ffff", TargetID: "beef", Archived: true, Words: sessioncmd.MCPVocabulary()}); err == nil || !strings.Contains(err.Error(), "calling session") {
		t.Fatalf("missing caller: %v", err)
	}
	if err := state.CreateSession(store.Session{ID: "dead", Name: "terminal", Tool: "terminal", Cwd: profile}); err != nil {
		t.Fatal(err)
	}
	if _, err := bound.Archive(sessioncmd.ArchiveRequest{CallerID: "cafe", TargetID: "dead", Archived: true, Words: sessioncmd.MCPVocabulary()}); err == nil || !strings.Contains(err.Error(), "terminal") {
		t.Fatalf("terminal target: %v", err)
	}

	description := describeOwner(t, profile)
	rejected := mustRPC(t, profile, testRequest("wrong-environment", operationArchiveSession, archiveArguments{
		CommandGuards: CommandGuards{ExpectedEnvironmentID: "other-profile", ExpectedOwnerInstanceID: description.Owner.InstanceID}, CallerID: "cafe", SessionID: "beef", Archived: true, Frontend: "cli",
	}))
	if rejected.OK || rejected.Error.Code != errorEnvironmentMismatch {
		t.Fatalf("environment guard = %+v", rejected)
	}
	old := time.Now().Add(-48 * time.Hour)
	ids := make([]int64, 3)
	for i := range ids {
		ids[i], err = state.Enqueue(store.InboxMessage{SessionID: "ffff", SenderID: "cafe", Body: fmt.Sprint(i), Fingerprint: fmt.Sprint(i), SentAt: old}, store.DefaultInboxLimits)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := state.MarkDelivered(ids[0], old); err != nil {
		t.Fatal(err)
	}
	if err := state.MarkDelivered(ids[1], time.Now()); err != nil {
		t.Fatal(err)
	}
	outer, err := tmux.NewWithSocket("am-poc-ui-" + uuid.NewString()[:8])
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	command := shellQuoteMigration(binary) + " migration-tui --dir " + shellQuoteMigration(profile) + " --tmux-socket " + shellQuoteMigration(driver.SocketName())
	if err := outer.Create("migration-ui", profile, command, map[string]string{"HOME": home}, 110, 30); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { outer.Kill("migration-ui") })
	deadline = time.Now().Add(10 * time.Second)
	var frame string
	dismissedWelcome := false
	for {
		frame, _ = outer.CapturePane("migration-ui")
		if !dismissedWelcome && strings.Contains(frame, "Welcome to agent-manager") {
			if err := outer.SendKeys("migration-ui", "Escape"); err != nil {
				t.Fatal(err)
			}
			dismissedWelcome = true
		}
		_, err = state.Message(ids[0], "cafe")
		if errors.Is(err, sql.ErrNoRows) && strings.Contains(frame, "migration-") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("actual TUI did not render and prune: %v\n%s", err, frame)
		}
		time.Sleep(25 * time.Millisecond)
	}
	logs, err := os.ReadFile(owner.logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(logs), "MIGRATION session.archive") != 2 || !strings.Contains(string(logs), "MIGRATION inbox.maintain") {
		t.Fatalf("owner did not execute actual frontend requests: %s", logs)
	}
	t.Logf("actual migrated TUI frame:\n%s", frame)
	for _, id := range ids[1:] {
		if _, err := state.Message(id, "cafe"); err != nil {
			t.Fatalf("maintenance removed retained message %d: %v", id, err)
		}
	}
	if err := outer.Kill("migration-ui"); err != nil {
		t.Fatal(err)
	}
	owner.stop(t)
	startOwnerProcess(t, profile, exec.Command(binary, "serve-migration", "--dir", profile, "--tmux-socket", driver.SocketName()))
	_, err = bound.Archive(sessioncmd.ArchiveRequest{CallerID: "cafe", TargetID: "beef", Archived: true, Words: sessioncmd.CLIVocabulary()})
	if application.CodeOf(err) != application.CodeOwnerInstanceMismatch {
		t.Fatalf("stale owner guard = %v", err)
	}
	row, err = state.Get("beef")
	if err != nil || row.Archived {
		t.Fatalf("stale owner mutated: %+v, %v", row, err)
	}
	result, err = mcpSession.CallTool(t.Context(), &mcp.CallToolParams{Name: "archive_session", Arguments: map[string]any{"session_id": "beef"}})
	if err != nil || !result.IsError {
		t.Fatalf("long-lived MCP must reject replacement owner: %+v, %v", result, err)
	}
	row, err = state.Get("beef")
	if err != nil || row.Archived {
		t.Fatalf("long-lived MCP fell back to local write: %+v, %v", row, err)
	}
	if err := bound.MaintainInbox(); application.CodeOf(err) != application.CodeOwnerInstanceMismatch {
		t.Fatalf("stale maintenance = %v", err)
	}

}

func shellQuoteMigration(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func TestMigrationServerRefusesProductionTmuxSocket(t *testing.T) {
	err := runMigrationOwner([]string{"--dir", t.TempDir(), "--tmux-socket", "agentmgr"}, io.Discard)
	if err == nil {
		t.Fatal("production socket accepted")
	}
}
