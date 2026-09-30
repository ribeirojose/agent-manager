package adapters

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
)

const readerTokenPrefix = "rt1."

type InventoryDocument struct {
	Scope   application.Scope `json:"scope"`
	Targets []TargetDocument  `json:"targets"`
}

type TargetDocument struct {
	TargetID  string            `json:"target_id"`
	Label     string            `json:"label"`
	Token     string            `json:"token,omitempty"`
	Owner     OwnerDocument     `json:"owner"`
	Freshness FreshnessDocument `json:"freshness"`
	Sessions  []SessionDocument `json:"sessions"`
}

type SessionDocument struct {
	TargetID string              `json:"target_id"`
	Token    string              `json:"token"`
	Session  SessionViewDocument `json:"session"`
}

type SessionViewDocument struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Tool          string `json:"tool"`
	Cwd           string `json:"cwd"`
	Group         string `json:"group"`
	Status        string `json:"status"`
	Archived      bool   `json:"archived"`
	InventoryOnly bool   `json:"inventory_only"`
}

type OwnerDocument struct {
	InstanceID    string   `json:"instance_id"`
	EnvironmentID string   `json:"environment_id"`
	Revision      int      `json:"revision"`
	Capabilities  []string `json:"capabilities"`
}

type FreshnessDocument struct {
	State       application.FreshnessState `json:"state"`
	RefreshedAt string                     `json:"refreshed_at,omitempty"`
	AttemptedAt string                     `json:"attempted_at,omitempty"`
	Failure     *FailureDocument           `json:"failure,omitempty"`
}

type FailureDocument struct {
	Code    application.Code `json:"code"`
	Message string           `json:"message"`
}

type MutationDocument struct {
	TargetID string              `json:"target_id"`
	Token    string              `json:"token"`
	Session  SessionViewDocument `json:"session"`
	Notice   string              `json:"notice,omitempty"`
}

type ConnectionDocument struct {
	ID                 string        `json:"id"`
	Label              string        `json:"label"`
	Route              RouteDocument `json:"route"`
	Generation         uint64        `json:"generation"`
	SavedEnvironmentID string        `json:"saved_environment_id,omitempty"`
}

type RouteDocument struct {
	Kind        application.AccessKind `json:"kind"`
	Profile     string                 `json:"profile"`
	Host        string                 `json:"host,omitempty"`
	OwnerBinary string                 `json:"owner_binary,omitempty"`
}

func PresentInventory(inventory application.Inventory) (InventoryDocument, error) {
	document := InventoryDocument{Scope: inventory.Scope, Targets: make([]TargetDocument, 0, len(inventory.Targets))}
	for _, target := range inventory.Targets {
		presented := TargetDocument{
			TargetID: EncodeOwnerTarget(target.Target), Label: target.Label,
			Owner: OwnerDocument{
				InstanceID: target.Owner.InstanceID, EnvironmentID: target.Owner.EnvironmentID,
				Revision: target.Owner.Revision, Capabilities: append([]string(nil), target.Owner.Capabilities...),
			},
			Freshness: presentFreshness(target.Freshness),
			Sessions:  make([]SessionDocument, 0, len(target.Sessions)),
		}
		if target.Token.OwnerInstanceID != "" {
			encoded, err := EncodeReaderToken(target.Token)
			if err != nil {
				return InventoryDocument{}, err
			}
			presented.Token = encoded
		}
		for _, session := range target.Sessions {
			encoded, err := EncodeReaderToken(session.Token)
			if err != nil {
				return InventoryDocument{}, err
			}
			presented.Sessions = append(presented.Sessions, SessionDocument{
				TargetID: EncodeSessionTarget(session.Target), Token: encoded, Session: presentSession(session.Session),
			})
		}
		document.Targets = append(document.Targets, presented)
	}
	return document, nil
}

func PresentMutation(result application.MutationResult) (MutationDocument, error) {
	token, err := EncodeReaderToken(result.Token)
	if err != nil {
		return MutationDocument{}, err
	}
	return MutationDocument{
		TargetID: EncodeSessionTarget(result.Target), Token: token,
		Session: presentSession(result.Session), Notice: result.Notice,
	}, nil
}

func presentSession(session application.Session) SessionViewDocument {
	return SessionViewDocument{
		ID: session.ID, Name: session.Name, Tool: session.Tool, Cwd: session.Cwd, Group: session.Group,
		Status: session.Status, Archived: session.Archived, InventoryOnly: session.InventoryOnly,
	}
}

func PresentConnection(connection application.Connection) ConnectionDocument {
	kind := connection.Endpoint.Access.Kind
	if kind == "" {
		kind = application.AccessLocal
	}
	return ConnectionDocument{
		ID: connection.ID, Label: connection.Label,
		Route: RouteDocument{
			Kind: kind, Profile: connection.Endpoint.Profile,
			Host: connection.Endpoint.Access.Host, OwnerBinary: connection.Endpoint.Access.OwnerBinary,
		},
		Generation: connection.Generation, SavedEnvironmentID: connection.SavedEnvironmentID,
	}
}

func presentFreshness(freshness application.Freshness) FreshnessDocument {
	document := FreshnessDocument{State: freshness.State}
	if !freshness.RefreshedAt.IsZero() {
		document.RefreshedAt = freshness.RefreshedAt.Format(time.RFC3339Nano)
	}
	if !freshness.AttemptedAt.IsZero() {
		document.AttemptedAt = freshness.AttemptedAt.Format(time.RFC3339Nano)
	}
	if freshness.Failure != nil {
		document.Failure = &FailureDocument{Code: freshness.Failure.Code, Message: freshness.Failure.Message}
	}
	return document
}

func EncodeOwnerTarget(target application.OwnerTarget) string {
	if target.Kind == application.TargetConnection {
		return "connection/" + url.PathEscape(target.ConnectionID)
	}
	return "device"
}

func DecodeOwnerTarget(value string) (application.OwnerTarget, error) {
	if value == "device" {
		return application.DeviceTarget(), nil
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 || parts[0] != "connection" {
		return application.OwnerTarget{}, application.NewError(application.CodeInvalidRequest, fmt.Sprintf("invalid owner target %q", value), nil)
	}
	id, err := url.PathUnescape(parts[1])
	if err != nil || id == "" {
		return application.OwnerTarget{}, application.NewError(application.CodeInvalidRequest, fmt.Sprintf("invalid connection target %q", value), err)
	}
	return application.ConnectionTarget(id), nil
}

func EncodeSessionTarget(target application.SessionTarget) string {
	if target.Owner.Kind == application.TargetConnection {
		return "connection/" + url.PathEscape(target.Owner.ConnectionID) + "/" + url.PathEscape(target.SessionID)
	}
	return "device/" + url.PathEscape(target.SessionID)
}

func DecodeSessionTarget(value string) (application.SessionTarget, error) {
	parts := strings.Split(value, "/")
	var owner application.OwnerTarget
	var rawSession string
	switch {
	case len(parts) == 2 && parts[0] == "device":
		owner = application.DeviceTarget()
		rawSession = parts[1]
	case len(parts) == 3 && parts[0] == "connection":
		connectionID, err := url.PathUnescape(parts[1])
		if err != nil || connectionID == "" {
			return application.SessionTarget{}, application.NewError(application.CodeInvalidRequest, fmt.Sprintf("invalid session target %q", value), err)
		}
		owner = application.ConnectionTarget(connectionID)
		rawSession = parts[2]
	default:
		return application.SessionTarget{}, application.NewError(application.CodeInvalidRequest, fmt.Sprintf("invalid session target %q", value), nil)
	}
	sessionID, err := url.PathUnescape(rawSession)
	if err != nil || sessionID == "" {
		return application.SessionTarget{}, application.NewError(application.CodeInvalidRequest, fmt.Sprintf("invalid session target %q", value), err)
	}
	return application.SessionTarget{Owner: owner, SessionID: sessionID}, nil
}

func EncodeReaderToken(token application.ReaderToken) (string, error) {
	document := readerTokenDTO{
		Target:     targetTokenDTO{Kind: token.Target.Kind, ConnectionID: token.Target.ConnectionID},
		Generation: token.Generation, RefreshSequence: token.RefreshSequence,
		EnvironmentID: token.EnvironmentID, OwnerInstanceID: token.OwnerInstanceID, Revision: token.Revision,
		CanCreateBoundFixture: token.CanCreateBoundFixture, CanRenameFixture: token.CanRenameFixture,
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return "", application.NewError(application.CodeOperationFailed, "encode reader token", err)
	}
	return readerTokenPrefix + rawBase64(payload), nil
}

func DecodeReaderToken(value string) (application.ReaderToken, error) {
	if !strings.HasPrefix(value, readerTokenPrefix) {
		return application.ReaderToken{}, application.NewError(application.CodeInvalidRequest, "reader token has an unsupported format", nil)
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, readerTokenPrefix))
	if err != nil {
		return application.ReaderToken{}, application.NewError(application.CodeInvalidRequest, "decode reader token", err)
	}
	var document readerTokenDTO
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return application.ReaderToken{}, application.NewError(application.CodeInvalidRequest, "decode reader token", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return application.ReaderToken{}, application.NewError(application.CodeInvalidRequest, "decode reader token", err)
	}
	target := application.OwnerTarget{Kind: document.Target.Kind, ConnectionID: document.Target.ConnectionID}
	if target.Kind != application.TargetDevice && target.Kind != application.TargetConnection {
		return application.ReaderToken{}, application.NewError(application.CodeInvalidRequest, "reader token has an invalid target kind", nil)
	}
	if target.Kind == application.TargetConnection && target.ConnectionID == "" {
		return application.ReaderToken{}, application.NewError(application.CodeInvalidRequest, "reader token is missing connection id", nil)
	}
	if document.EnvironmentID == "" || document.OwnerInstanceID == "" {
		return application.ReaderToken{}, application.NewError(application.CodeInvalidRequest, "reader token is missing owner identity", nil)
	}
	return application.ReaderToken{
		Target: target, Generation: document.Generation, RefreshSequence: document.RefreshSequence,
		EnvironmentID: document.EnvironmentID, OwnerInstanceID: document.OwnerInstanceID, Revision: document.Revision,
		CanCreateBoundFixture: document.CanCreateBoundFixture, CanRenameFixture: document.CanRenameFixture,
	}, nil
}

func rawBase64(payload []byte) string { return base64.RawURLEncoding.EncodeToString(payload) }

type readerTokenDTO struct {
	Target                targetTokenDTO `json:"target"`
	Generation            uint64         `json:"generation"`
	RefreshSequence       uint64         `json:"refresh_sequence"`
	EnvironmentID         string         `json:"environment_id"`
	OwnerInstanceID       string         `json:"owner_instance_id"`
	Revision              int            `json:"revision"`
	CanCreateBoundFixture bool           `json:"can_create_bound_fixture"`
	CanRenameFixture      bool           `json:"can_rename_fixture"`
}

type targetTokenDTO struct {
	Kind         application.TargetKind `json:"kind"`
	ConnectionID string                 `json:"connection_id,omitempty"`
}
