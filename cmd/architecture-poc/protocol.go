package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/YoanWai/agent-manager/internal/store"
)

const (
	protocolMajor = 1

	operationDescribe           = "owner.describe"
	operationListSessions       = "sessions.list"
	operationCreateFixture      = "fixture_session.create"
	operationCreateBoundFixture = "fixture_session.create_bound"
	operationRenameFixture      = "fixture_session.rename"

	capabilityDescribe           = "owner.describe"
	capabilitySessionsRead       = "sessions.read"
	capabilityFixtureCreate      = "fixture_session.create"
	capabilityFixtureCreateBound = "fixture_session.create_bound"
	capabilityFixtureRename      = "fixture_session.rename"

	errorOwnerAlreadyRunning   = "owner_already_running"
	errorUnsupportedProtocol   = "unsupported_protocol"
	errorUnknownOperation      = "unknown_operation"
	errorInvalidRequest        = "invalid_request"
	errorMissingCapability     = "missing_capability"
	errorEnvironmentMismatch   = "environment_mismatch"
	errorOwnerInstanceMismatch = "owner_instance_mismatch"
	errorFixtureRequired       = "fixture_required"
	errorOperationFailed       = "operation_failed"

	fixtureStatus = "fixture"
)

type Request struct {
	ProtocolMajor int             `json:"protocol_major"`
	RequestID     string          `json:"request_id"`
	Operation     string          `json:"operation"`
	Arguments     json.RawMessage `json:"arguments"`
}

type Response struct {
	ProtocolMajor int             `json:"protocol_major"`
	RequestID     string          `json:"request_id"`
	OK            bool            `json:"ok"`
	Owner         OwnerInfo       `json:"owner"`
	Result        json.RawMessage `json:"result,omitempty"`
	Error         *RPCError       `json:"error,omitempty"`
}

type OwnerInfo struct {
	InstanceID    string   `json:"instance_id"`
	EnvironmentID string   `json:"environment_id"`
	Revision      int      `json:"revision"`
	Capabilities  []string `json:"capabilities"`
}

type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type DescribeArguments struct{}

type DescribeResult struct {
	Kind           string   `json:"kind"`
	ProtocolMajor  int      `json:"protocol_major"`
	Capabilities   []string `json:"capabilities"`
	InventoryOwner bool     `json:"inventory_owner"`
}

type ListSessionsArguments struct {
	IncludeArchived bool `json:"include_archived"`
}

type ListSessionsResult struct {
	Kind     string        `json:"kind"`
	Sessions []SessionView `json:"sessions"`
}

type FixtureSession struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Tool  string `json:"tool"`
	Cwd   string `json:"cwd"`
	Group string `json:"group"`
}

type CreateFixtureArguments struct {
	ExpectedEnvironmentID string         `json:"expected_environment_id"`
	Session               FixtureSession `json:"session"`
}

type CommandGuards struct {
	ExpectedEnvironmentID   string `json:"expected_environment_id"`
	ExpectedOwnerInstanceID string `json:"expected_owner_instance_id"`
}

type BoundCreateFixtureArguments struct {
	CommandGuards
	Session FixtureSession `json:"session"`
}

type RenameFixtureArguments struct {
	CommandGuards
	SessionID string `json:"session_id"`
	Name      string `json:"name"`
}

type CreateFixtureResult struct {
	Kind    string      `json:"kind"`
	Session SessionView `json:"session"`
	Notice  string      `json:"notice"`
}

type RenameFixtureResult struct {
	Kind    string      `json:"kind"`
	Session SessionView `json:"session"`
	Notice  string      `json:"notice"`
}

type SessionView struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Tool          string `json:"tool"`
	Cwd           string `json:"cwd"`
	Group         string `json:"group"`
	Status        string `json:"status"`
	Archived      bool   `json:"archived"`
	InventoryOnly bool   `json:"inventory_only"`
}

type ownerState struct {
	info  OwnerInfo
	store *store.Store
}

func capabilitiesForRevision(revision int) []string {
	capabilities := []string{capabilityDescribe, capabilitySessionsRead}
	if revision == 2 {
		capabilities = append(capabilities, capabilityFixtureCreate)
	}
	if revision >= 3 {
		capabilities = append(capabilities, capabilityFixtureCreateBound, capabilityFixtureRename)
	}
	return capabilities
}

func (owner *ownerState) response(requestID string) Response {
	info := owner.info
	info.Capabilities = append([]string(nil), owner.info.Capabilities...)
	return Response{ProtocolMajor: protocolMajor, RequestID: requestID, Owner: info}
}

func (owner *ownerState) reject(requestID, code, message string) Response {
	response := owner.response(requestID)
	response.Error = &RPCError{Code: code, Message: message}
	return response
}

func (owner *ownerState) succeed(requestID string, result any) Response {
	response := owner.response(requestID)
	payload, err := json.Marshal(result)
	if err != nil {
		return owner.reject(requestID, errorOperationFailed, "encode operation result: "+err.Error())
	}
	response.OK = true
	response.Result = payload
	return response
}

func (owner *ownerState) dispatch(request Request) Response {
	if request.ProtocolMajor != protocolMajor {
		return owner.reject(request.RequestID, errorUnsupportedProtocol,
			fmt.Sprintf("owner supports protocol major %d, request uses %d", protocolMajor, request.ProtocolMajor))
	}
	if strings.TrimSpace(request.RequestID) == "" {
		return owner.reject(request.RequestID, errorInvalidRequest, "request_id is required")
	}

	switch request.Operation {
	case operationDescribe:
		var arguments DescribeArguments
		if err := decodeArguments(request.Arguments, &arguments); err != nil {
			return owner.reject(request.RequestID, errorInvalidRequest, err.Error())
		}
		return owner.succeed(request.RequestID, DescribeResult{
			Kind: operationDescribe, ProtocolMajor: protocolMajor,
			Capabilities: append([]string(nil), owner.info.Capabilities...), InventoryOwner: true,
		})
	case operationListSessions:
		var arguments ListSessionsArguments
		if err := decodeArguments(request.Arguments, &arguments); err != nil {
			return owner.reject(request.RequestID, errorInvalidRequest, err.Error())
		}
		return owner.list(request.RequestID, arguments)
	case operationCreateFixture:
		if !owner.hasCapability(capabilityFixtureCreate) {
			return owner.reject(request.RequestID, errorMissingCapability,
				fmt.Sprintf("owner revision %d does not support %s", owner.info.Revision, operationCreateFixture))
		}
		var arguments CreateFixtureArguments
		if err := decodeArguments(request.Arguments, &arguments); err != nil {
			return owner.reject(request.RequestID, errorInvalidRequest, err.Error())
		}
		return owner.createFixture(request.RequestID, arguments)
	case operationCreateBoundFixture:
		if !owner.hasCapability(capabilityFixtureCreateBound) {
			return owner.reject(request.RequestID, errorMissingCapability,
				fmt.Sprintf("owner revision %d does not support %s", owner.info.Revision, operationCreateBoundFixture))
		}
		var arguments BoundCreateFixtureArguments
		if err := decodeArguments(request.Arguments, &arguments); err != nil {
			return owner.reject(request.RequestID, errorInvalidRequest, err.Error())
		}
		return owner.createBoundFixture(request.RequestID, arguments)
	case operationRenameFixture:
		if !owner.hasCapability(capabilityFixtureRename) {
			return owner.reject(request.RequestID, errorMissingCapability,
				fmt.Sprintf("owner revision %d does not support %s", owner.info.Revision, operationRenameFixture))
		}
		var arguments RenameFixtureArguments
		if err := decodeArguments(request.Arguments, &arguments); err != nil {
			return owner.reject(request.RequestID, errorInvalidRequest, err.Error())
		}
		return owner.renameFixture(request.RequestID, arguments)
	default:
		return owner.reject(request.RequestID, errorUnknownOperation,
			fmt.Sprintf("owner does not recognize operation %q", request.Operation))
	}
}

func (owner *ownerState) hasCapability(capability string) bool {
	for _, current := range owner.info.Capabilities {
		if current == capability {
			return true
		}
	}
	return false
}

func (owner *ownerState) list(requestID string, arguments ListSessionsArguments) Response {
	sessions, err := owner.store.ListSessions(arguments.IncludeArchived)
	if err != nil {
		return owner.reject(requestID, errorOperationFailed, "list sessions: "+err.Error())
	}
	views := make([]SessionView, 0, len(sessions))
	for _, session := range sessions {
		views = append(views, viewOf(session))
	}
	return owner.succeed(requestID, ListSessionsResult{Kind: operationListSessions, Sessions: views})
}

func (owner *ownerState) createFixture(requestID string, arguments CreateFixtureArguments) Response {
	if arguments.ExpectedEnvironmentID != owner.info.EnvironmentID {
		return owner.reject(requestID, errorEnvironmentMismatch,
			fmt.Sprintf("expected environment %q does not match owner environment %q",
				arguments.ExpectedEnvironmentID, owner.info.EnvironmentID))
	}
	return owner.insertFixture(requestID, operationCreateFixture, arguments.Session)
}

func (owner *ownerState) createBoundFixture(requestID string, arguments BoundCreateFixtureArguments) Response {
	if code, message := owner.guardError(arguments.CommandGuards); code != "" {
		return owner.reject(requestID, code, message)
	}
	return owner.insertFixture(requestID, operationCreateBoundFixture, arguments.Session)
}

func (owner *ownerState) insertFixture(requestID, operation string, candidate FixtureSession) Response {
	fixture, err := normalizeFixture(candidate)
	if err != nil {
		return owner.reject(requestID, errorInvalidRequest, err.Error())
	}
	session := store.Session{
		ID: fixture.ID, Name: fixture.Name, Tool: fixture.Tool,
		Cwd: fixture.Cwd, Group: fixture.Group, Status: fixtureStatus,
	}
	if err := owner.store.CreateSession(session); err != nil {
		return owner.reject(requestID, errorOperationFailed, "create fixture session: "+err.Error())
	}
	return owner.succeed(requestID, CreateFixtureResult{
		Kind:    operation,
		Session: viewOf(session),
		Notice:  "inventory-only fixture created through store.CreateSession; this command did not launch an agent or a tmux session",
	})
}

func (owner *ownerState) renameFixture(requestID string, arguments RenameFixtureArguments) Response {
	if code, message := owner.guardError(arguments.CommandGuards); code != "" {
		return owner.reject(requestID, code, message)
	}
	sessionID := strings.TrimSpace(arguments.SessionID)
	if sessionID == "" {
		return owner.reject(requestID, errorInvalidRequest, "session_id is required")
	}
	name := strings.TrimSpace(arguments.Name)
	if name == "" {
		return owner.reject(requestID, errorInvalidRequest, "name is required")
	}
	session, err := owner.store.Get(sessionID)
	if err != nil {
		return owner.reject(requestID, errorOperationFailed, "get fixture session: "+err.Error())
	}
	if !viewOf(session).InventoryOnly {
		return owner.reject(requestID, errorFixtureRequired,
			fmt.Sprintf("session %q is not an inventory-only fixture", sessionID))
	}
	if err := owner.store.RenameSession(sessionID, name); err != nil {
		return owner.reject(requestID, errorOperationFailed, "rename fixture session: "+err.Error())
	}
	session.Name = name
	return owner.succeed(requestID, RenameFixtureResult{
		Kind: operationRenameFixture, Session: viewOf(session),
		Notice: "inventory-only fixture metadata renamed; no agent or tmux session was changed",
	})
}

func (owner *ownerState) guardError(guards CommandGuards) (string, string) {
	if guards.ExpectedEnvironmentID != owner.info.EnvironmentID {
		return errorEnvironmentMismatch, fmt.Sprintf("expected environment %q does not match owner environment %q",
			guards.ExpectedEnvironmentID, owner.info.EnvironmentID)
	}
	if guards.ExpectedOwnerInstanceID != owner.info.InstanceID {
		return errorOwnerInstanceMismatch, fmt.Sprintf("expected owner instance %q does not match current owner instance %q",
			guards.ExpectedOwnerInstanceID, owner.info.InstanceID)
	}
	return "", ""
}

func normalizeFixture(session FixtureSession) (FixtureSession, error) {
	fields := []*string{&session.ID, &session.Name, &session.Tool, &session.Cwd, &session.Group}
	names := []string{"session.id", "session.name", "session.tool", "session.cwd", "session.group"}
	for index, field := range fields {
		*field = strings.TrimSpace(*field)
		if *field == "" {
			return FixtureSession{}, fmt.Errorf("%s is required", names[index])
		}
	}
	return session, nil
}

func viewOf(session store.Session) SessionView {
	return SessionView{
		ID: session.ID, Name: session.Name, Tool: session.Tool, Cwd: session.Cwd,
		Group: session.Group, Status: session.Status, Archived: session.Archived,
		InventoryOnly: session.Status == fixtureStatus && session.TmuxSocket == "",
	}
}

func decodeRequest(reader io.Reader) (Request, error) {
	var request Request
	if err := decodeStrict(reader, &request); err != nil {
		return Request{}, fmt.Errorf("decode request: %w", err)
	}
	return request, nil
}

func decodeArguments(payload json.RawMessage, target any) error {
	if len(bytes.TrimSpace(payload)) == 0 || bytes.Equal(bytes.TrimSpace(payload), []byte("null")) {
		payload = []byte("{}")
	}
	if err := decodeStrict(bytes.NewReader(payload), target); err != nil {
		return fmt.Errorf("decode arguments: %w", err)
	}
	return nil
}

func decodeStrict(reader io.Reader, target any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func decodeResponse(reader io.Reader) (Response, error) {
	var response Response
	decoder := json.NewDecoder(reader)
	if err := decoder.Decode(&response); err != nil {
		return Response{}, fmt.Errorf("decode response: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Response{}, errors.New("decode response: multiple JSON values")
		}
		return Response{}, fmt.Errorf("decode response: %w", err)
	}
	if response.ProtocolMajor != protocolMajor {
		return Response{}, fmt.Errorf("unsupported response protocol major %d; client supports %d", response.ProtocolMajor, protocolMajor)
	}
	return response, nil
}
