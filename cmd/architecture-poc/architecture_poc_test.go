package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const helperProcessEnv = "AGENT_MANAGER_ARCHITECTURE_POC_HELPER"

func TestArchitecturePOCProcess(t *testing.T) {
	if os.Getenv(helperProcessEnv) != "1" {
		return
	}
	separator := slices.Index(os.Args, "--")
	if separator < 0 {
		fmt.Fprintln(os.Stderr, "architecture-poc: helper has no argument separator")
		os.Exit(2)
	}
	if err := runCLI(os.Args[separator+1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "architecture-poc:", err)
		os.Exit(1)
	}
	os.Exit(0)
}

type testOwner struct {
	cmd      *exec.Cmd
	dir      string
	logPath  string
	logFile  *os.File
	stopOnce sync.Once
}

func startTestOwner(t *testing.T, dir string, revision int) *testOwner {
	t.Helper()
	logPath := filepath.Join(t.TempDir(), "owner.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create owner log: %v", err)
	}
	cmd := helperCommand("serve", "--dir", dir, "--revision", strconv.Itoa(revision))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		t.Fatalf("start owner: %v", err)
	}
	owner := &testOwner{cmd: cmd, dir: dir, logPath: logPath, logFile: logFile}
	t.Cleanup(func() { owner.stop(t) })

	socket, err := socketPathForProfile(dir)
	if err != nil {
		t.Fatalf("resolve owner socket: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Lstat(socket); err == nil {
			response, _, callErr := invokeRPC(dir, testRequest("ready", operationDescribe, DescribeArguments{}))
			if callErr == nil && response.OK {
				break
			}
		}
		if time.Now().After(deadline) {
			owner.stop(t)
			contents, _ := os.ReadFile(logPath)
			t.Fatalf("owner did not become ready: %s", contents)
		}
		time.Sleep(20 * time.Millisecond)
	}
	return owner
}

func (owner *testOwner) stop(t *testing.T) {
	t.Helper()
	owner.stopOnce.Do(func() {
		if err := owner.cmd.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
			t.Errorf("interrupt owner: %v", err)
		}
		done := make(chan error, 1)
		go func() { done <- owner.cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				contents, _ := os.ReadFile(owner.logPath)
				t.Errorf("owner shutdown: %v\n%s", err, contents)
			}
		case <-time.After(3 * time.Second):
			if err := owner.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
				t.Errorf("kill stuck owner: %v", err)
			}
			<-done
			t.Error("owner did not shut down after interrupt")
		}
		if err := owner.logFile.Close(); err != nil {
			t.Errorf("close owner log: %v", err)
		}
		socket, err := socketPathForProfile(owner.dir)
		if err != nil {
			t.Errorf("resolve socket after shutdown: %v", err)
			return
		}
		if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("socket survived owner shutdown: %v", err)
		}
	})
}

func helperCommand(args ...string) *exec.Cmd {
	all := []string{"-test.run=^TestArchitecturePOCProcess$", "--"}
	all = append(all, args...)
	cmd := exec.Command(os.Args[0], all...)
	cmd.Env = append(os.Environ(), helperProcessEnv+"=1")
	return cmd
}

func invokeRPC(dir string, request Request) (Response, string, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return Response{}, "", err
	}
	cmd := helperCommand("rpc", "--dir", dir)
	cmd.Stdin = bytes.NewReader(append(payload, '\n'))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return Response{}, string(output), fmt.Errorf("rpc process: %w", err)
	}
	response, err := decodeResponse(bytes.NewReader(output))
	return response, string(output), err
}

func mustRPC(t *testing.T, dir string, request Request) Response {
	t.Helper()
	response, output, err := invokeRPC(dir, request)
	if err != nil {
		t.Fatalf("rpc: %v\n%s", err, output)
	}
	return response
}

func testRequest(id, operation string, arguments any) Request {
	payload, err := json.Marshal(arguments)
	if err != nil {
		panic(err)
	}
	return Request{
		ProtocolMajor: protocolMajor,
		RequestID:     id,
		Operation:     operation,
		Arguments:     payload,
	}
}

func describeOwner(t *testing.T, dir string) Response {
	t.Helper()
	response := mustRPC(t, dir, testRequest("describe", operationDescribe, DescribeArguments{}))
	if !response.OK {
		t.Fatalf("describe rejected: %+v", response.Error)
	}
	return response
}

func listSessions(t *testing.T, dir string) ListSessionsResult {
	t.Helper()
	response := mustRPC(t, dir, testRequest("list", operationListSessions, ListSessionsArguments{}))
	if !response.OK {
		t.Fatalf("list rejected: %+v", response.Error)
	}
	var result ListSessionsResult
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("decode list result: %v", err)
	}
	return result
}

func fixtureArguments(environmentID, id string) CreateFixtureArguments {
	return CreateFixtureArguments{
		ExpectedEnvironmentID: environmentID,
		Session: FixtureSession{
			ID:    id,
			Name:  "fixture-session",
			Tool:  "codex",
			Cwd:   "/tmp/architecture-poc-fixture",
			Group: "architecture-poc",
		},
	}
}

func TestOwnerLockExcludesOnlyTheSameProfile(t *testing.T) {
	firstDir := t.TempDir()
	first := startTestOwner(t, firstDir, 2)

	second := helperCommand("serve", "--dir", firstDir, "--revision", "2")
	output, err := second.CombinedOutput()
	if err == nil {
		t.Fatalf("second owner started for one profile: %s", output)
	}
	if !strings.Contains(string(output), errorOwnerAlreadyRunning) {
		t.Fatalf("second owner error = %q, want %q", output, errorOwnerAlreadyRunning)
	}

	otherDir := t.TempDir()
	other := startTestOwner(t, otherDir, 1)
	if firstOwner, otherOwner := describeOwner(t, firstDir).Owner, describeOwner(t, otherDir).Owner; firstOwner.InstanceID == otherOwner.InstanceID {
		t.Fatalf("independent profiles share owner instance %q", firstOwner.InstanceID)
	}
	first.stop(t)
	other.stop(t)
}

func TestEnvironmentMismatchRejectsCreateWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	owner := startTestOwner(t, dir, 2)
	described := describeOwner(t, dir)
	if !slices.Contains(described.Owner.Capabilities, capabilityFixtureCreate) {
		t.Fatalf("revision 2 capabilities = %v", described.Owner.Capabilities)
	}

	rejected := mustRPC(t, dir, testRequest("wrong-environment", operationCreateFixture, fixtureArguments("env-wrong", "fixture-rejected")))
	if rejected.OK || rejected.Error == nil || rejected.Error.Code != errorEnvironmentMismatch {
		t.Fatalf("environment mismatch response = %+v", rejected)
	}
	if got := len(listSessions(t, dir).Sessions); got != 0 {
		t.Fatalf("rejected create wrote %d sessions", got)
	}

	accepted := mustRPC(t, dir, testRequest("right-environment", operationCreateFixture, fixtureArguments(described.Owner.EnvironmentID, "fixture-created")))
	if !accepted.OK {
		t.Fatalf("guarded fixture create rejected: %+v", accepted.Error)
	}
	var created CreateFixtureResult
	if err := json.Unmarshal(accepted.Result, &created); err != nil {
		t.Fatalf("decode create result: %v", err)
	}
	if !created.Session.InventoryOnly || created.Session.Status != fixtureStatus || !strings.Contains(created.Notice, "did not launch") {
		t.Fatalf("create result does not label the fixture: %+v", created)
	}
	sessions := listSessions(t, dir).Sessions
	if len(sessions) != 1 || sessions[0].ID != "fixture-created" || !sessions[0].InventoryOnly {
		t.Fatalf("sessions after guarded create = %+v", sessions)
	}
	listSessions(t, dir)
	if got := len(listSessions(t, dir).Sessions); got != 1 {
		t.Fatalf("reads changed inventory to %d sessions", got)
	}
	owner.stop(t)
}

func TestNewBridgeCannotElevateAnOldOwner(t *testing.T) {
	dir := t.TempDir()
	owner := startTestOwner(t, dir, 1)
	described := describeOwner(t, dir)
	if described.Owner.Revision != 1 || slices.Contains(described.Owner.Capabilities, capabilityFixtureCreate) {
		t.Fatalf("revision 1 owner metadata = %+v", described.Owner)
	}

	response := mustRPC(t, dir, testRequest("new-client-create", operationCreateFixture, fixtureArguments(described.Owner.EnvironmentID, "must-not-exist")))
	if response.OK || response.Error == nil || response.Error.Code != errorMissingCapability {
		t.Fatalf("old owner accepted new operation: %+v", response)
	}
	if response.Owner.Revision != 1 || slices.Contains(response.Owner.Capabilities, capabilityFixtureCreate) {
		t.Fatalf("bridge elevated owner metadata: %+v", response.Owner)
	}
	if got := len(listSessions(t, dir).Sessions); got != 0 {
		t.Fatalf("old owner rejection wrote %d sessions", got)
	}
	owner.stop(t)
}

func TestProtocolMajorMismatchIsDeniedWithoutWriting(t *testing.T) {
	dir := t.TempDir()
	owner := startTestOwner(t, dir, 2)
	request := testRequest("future-major", operationCreateFixture, fixtureArguments("irrelevant", "must-not-exist"))
	request.ProtocolMajor = protocolMajor + 1
	response := mustRPC(t, dir, request)
	if response.OK || response.Error == nil || response.Error.Code != errorUnsupportedProtocol {
		t.Fatalf("protocol mismatch response = %+v", response)
	}
	if response.ProtocolMajor != protocolMajor {
		t.Fatalf("owner reported protocol %d, want %d", response.ProtocolMajor, protocolMajor)
	}
	if got := len(listSessions(t, dir).Sessions); got != 0 {
		t.Fatalf("protocol rejection wrote %d sessions", got)
	}
	owner.stop(t)
}

func TestResponseDecoderIgnoresAdditiveFields(t *testing.T) {
	raw := `{
		"protocol_major": 1,
		"request_id": "old-reader",
		"ok": true,
		"owner": {
			"instance_id": "owner-1",
			"environment_id": "env-1",
			"revision": 2,
			"capabilities": ["owner.describe"],
			"future_owner_field": {"nested": true}
		},
		"result": {"kind": "owner.describe"},
		"future_envelope_field": [1, 2, 3]
	}`
	response, err := decodeResponse(strings.NewReader(raw))
	if err != nil {
		t.Fatalf("decode response with additive fields: %v", err)
	}
	if !response.OK || response.Owner.InstanceID != "owner-1" {
		t.Fatalf("decoded response = %+v", response)
	}
}

func TestResponseDecoderRejectsAnUnsupportedMajor(t *testing.T) {
	raw := `{
		"protocol_major": 2,
		"request_id": "future-owner",
		"ok": true,
		"owner": {
			"instance_id": "owner-2",
			"environment_id": "env-2",
			"revision": 3,
			"capabilities": ["owner.describe"]
		},
		"result": {"kind": "owner.describe"}
	}`
	if _, err := decodeResponse(strings.NewReader(raw)); err == nil || !strings.Contains(err.Error(), "unsupported response protocol major 2") {
		t.Fatalf("future response major error = %v", err)
	}
}

func TestSocketRuntimeDirectoryRejectsUnsafePermissions(t *testing.T) {
	base := t.TempDir()
	unsafe := filepath.Join(base, "runtime")
	if err := os.Mkdir(unsafe, 0o755); err != nil {
		t.Fatalf("mkdir unsafe runtime: %v", err)
	}
	t.Setenv(runtimeDirectoryOverrideEnv, unsafe)
	if _, err := socketPathForProfile(t.TempDir()); err == nil || !strings.Contains(err.Error(), "0700") {
		t.Fatalf("unsafe runtime directory error = %v", err)
	}
}

func TestServeStopsWhenItsContextIsCanceled(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, dir, 1, ioDiscard{}) }()
	socket, err := socketPathForProfile(dir)
	if err != nil {
		t.Fatalf("socket path: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Lstat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("in-process owner did not listen")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve after cancellation: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve ignored context cancellation")
	}
	if _, err := os.Lstat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket after cancellation: %v", err)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
