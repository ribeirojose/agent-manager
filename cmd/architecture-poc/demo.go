package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

type demoEvent struct {
	Case     string   `json:"case"`
	Response Response `json:"response"`
}

func runLocalDemo(stdout, stderr io.Writer) error {
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve demo executable: %w", err)
	}
	root, err := os.MkdirTemp("", "agent-manager-architecture-poc-demo-")
	if err != nil {
		return fmt.Errorf("create demo directory: %w", err)
	}
	defer os.RemoveAll(root)

	oldProfile := filepath.Join(root, "revision-1")
	if err := os.Mkdir(oldProfile, 0o700); err != nil {
		return err
	}
	if err := withDemoOwner(executable, oldProfile, 1, stderr, func() error {
		described, err := demoCall(executable, oldProfile, demoRequest(1, operationDescribe, DescribeArguments{}))
		if err != nil {
			return err
		}
		if err := writeDemoEvent(stdout, "revision-1 exposes inventory-read RPCs", described); err != nil {
			return err
		}
		created, err := demoCall(executable, oldProfile,
			demoRequest(2, operationCreateFixture, demoFixture(described.Owner.EnvironmentID, "old-owner-rejected")))
		if err != nil {
			return err
		}
		if err := writeDemoEvent(stdout, "new bridge cannot create through revision 1", created); err != nil {
			return err
		}
		listed, err := demoCall(executable, oldProfile, demoRequest(3, operationListSessions, ListSessionsArguments{}))
		if err != nil {
			return err
		}
		return writeDemoEvent(stdout, "revision-1 rejection leaves inventory empty", listed)
	}); err != nil {
		return err
	}

	newProfile := filepath.Join(root, "revision-2")
	if err := os.Mkdir(newProfile, 0o700); err != nil {
		return err
	}
	return withDemoOwner(executable, newProfile, 2, stderr, func() error {
		described, err := demoCall(executable, newProfile, demoRequest(4, operationDescribe, DescribeArguments{}))
		if err != nil {
			return err
		}
		if err := writeDemoEvent(stdout, "revision-2 describes guarded fixture creation", described); err != nil {
			return err
		}
		mismatch, err := demoCall(executable, newProfile,
			demoRequest(5, operationCreateFixture, demoFixture("env-wrong", "wrong-environment")))
		if err != nil {
			return err
		}
		if err := writeDemoEvent(stdout, "revision-2 rejects a changed environment", mismatch); err != nil {
			return err
		}
		created, err := demoCall(executable, newProfile,
			demoRequest(6, operationCreateFixture, demoFixture(described.Owner.EnvironmentID, "fixture-created")))
		if err != nil {
			return err
		}
		if err := writeDemoEvent(stdout, "revision-2 creates an inventory-only fixture", created); err != nil {
			return err
		}
		listed, err := demoCall(executable, newProfile, demoRequest(7, operationListSessions, ListSessionsArguments{}))
		if err != nil {
			return err
		}
		if err := writeDemoEvent(stdout, "revision-2 reads the fixture", listed); err != nil {
			return err
		}
		future := demoRequest(8, operationDescribe, DescribeArguments{})
		future.ProtocolMajor = protocolMajor + 1
		rejected, err := demoCall(executable, newProfile, future)
		if err != nil {
			return err
		}
		return writeDemoEvent(stdout, "owner rejects an unsupported request major", rejected)
	})
}

func demoRequest(sequence int, operation string, arguments any) Request {
	payload, err := json.Marshal(arguments)
	if err != nil {
		panic(err)
	}
	return Request{
		ProtocolMajor: protocolMajor, RequestID: "demo-" + strconv.Itoa(sequence),
		Operation: operation, Arguments: payload,
	}
}

func demoFixture(environmentID, id string) CreateFixtureArguments {
	return CreateFixtureArguments{
		ExpectedEnvironmentID: environmentID,
		Session: FixtureSession{
			ID: id, Name: "fixture-session", Tool: "codex",
			Cwd: "/tmp/architecture-poc-fixture", Group: "architecture-poc",
		},
	}
}

func writeDemoEvent(writer io.Writer, name string, response Response) error {
	return json.NewEncoder(writer).Encode(demoEvent{Case: name, Response: response})
}

func demoCall(executable, dir string, request Request) (Response, error) {
	payload, err := json.Marshal(request)
	if err != nil {
		return Response{}, err
	}
	command := exec.Command(executable, "rpc", "--dir", dir)
	command.Stdin = bytes.NewReader(payload)
	output, err := command.CombinedOutput()
	if err != nil {
		return Response{}, fmt.Errorf("demo rpc: %w: %s", err, output)
	}
	response, err := decodeResponse(bytes.NewReader(output))
	if err != nil {
		return Response{}, fmt.Errorf("demo rpc output: %w", err)
	}
	return response, nil
}

type demoOwnerProcess struct {
	command *exec.Cmd
	log     *os.File
	logPath string
}

func withDemoOwner(executable, dir string, revision int, stderr io.Writer, work func() error) (resultErr error) {
	owner, err := startDemoOwner(executable, dir, revision)
	if err != nil {
		return err
	}
	defer func() {
		if err := owner.stop(); err != nil && resultErr == nil {
			resultErr = err
		}
	}()
	return work()
}

func startDemoOwner(executable, dir string, revision int) (*demoOwnerProcess, error) {
	logPath := filepath.Join(dir, "owner.log")
	log, err := os.Create(logPath)
	if err != nil {
		return nil, err
	}
	command := exec.Command(executable, "serve", "--dir", dir, "--revision", strconv.Itoa(revision))
	command.Stdout = log
	command.Stderr = log
	if err := command.Start(); err != nil {
		log.Close()
		return nil, err
	}
	owner := &demoOwnerProcess{command: command, log: log, logPath: logPath}
	socket, err := socketPathForProfile(dir)
	if err != nil {
		owner.stop()
		return nil, err
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Lstat(socket); err == nil {
			if response, callErr := demoCall(executable, dir, demoRequest(0, operationDescribe, DescribeArguments{})); callErr == nil && response.OK {
				return owner, nil
			}
		}
		if time.Now().After(deadline) {
			_ = owner.stop()
			contents, _ := os.ReadFile(logPath)
			return nil, fmt.Errorf("demo owner did not become ready: %s", contents)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (owner *demoOwnerProcess) stop() error {
	if owner.command.ProcessState != nil {
		return nil
	}
	if err := owner.command.Process.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- owner.command.Wait() }()
	select {
	case err := <-done:
		owner.log.Close()
		return err
	case <-time.After(3 * time.Second):
		_ = owner.command.Process.Kill()
		<-done
		owner.log.Close()
		return errors.New("demo owner did not stop after interrupt")
	}
}
