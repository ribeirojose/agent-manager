package adapters

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/YoanWai/agent-manager/internal/architecturepoc/application"
	"golang.org/x/sys/unix"
)

const workspaceSchemaVersion = 1

type FileRepository struct {
	path     string
	deviceID string
}

func NewFileRepository(path, deviceID string) *FileRepository {
	return &FileRepository{path: path, deviceID: deviceID}
}

func (repository *FileRepository) View(ctx context.Context, work func(application.WorkspaceState) error) error {
	return repository.withLock(ctx, func() error {
		state, err := repository.load()
		if err != nil {
			return err
		}
		return work(state)
	})
}

func (repository *FileRepository) Update(ctx context.Context, work func(*application.WorkspaceState) error) error {
	return repository.withLock(ctx, func() error {
		state, err := repository.load()
		if err != nil {
			return err
		}
		if err := work(&state); err != nil {
			return err
		}
		return repository.save(state)
	})
}

func (repository *FileRepository) withLock(ctx context.Context, work func() error) error {
	if repository.path == "" {
		return application.NewError(application.CodeInvalidRequest, "workspace config path is required", nil)
	}
	if err := os.MkdirAll(filepath.Dir(repository.path), 0o700); err != nil {
		return application.NewError(application.CodeOperationFailed, "create workspace config directory", err)
	}
	lockPath := repository.path + ".lock"
	fd, err := unix.Open(lockPath, unix.O_CREAT|unix.O_RDWR|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return application.NewError(application.CodeOperationFailed, "open workspace config lock", err)
	}
	lock := os.NewFile(uintptr(fd), lockPath)
	defer lock.Close()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return application.NewError(application.CodeOperationFailed, "inspect workspace config lock", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Uid != uint32(os.Getuid()) || stat.Mode&0o077 != 0 {
		return application.NewError(application.CodeOperationFailed, "workspace config lock is not a private regular file owned by this user", nil)
	}
	for {
		err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return application.NewError(application.CodeOperationFailed, "acquire workspace config lock", err)
		}
		select {
		case <-ctx.Done():
			return application.NewError(application.CodeOperationFailed, "wait for workspace config lock", ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer unix.Flock(fd, unix.LOCK_UN)
	return work()
}

func (repository *FileRepository) load() (application.WorkspaceState, error) {
	contents, err := os.ReadFile(repository.path)
	if errors.Is(err, os.ErrNotExist) {
		return application.WorkspaceState{DeviceID: repository.deviceID, NextGeneration: 1}, nil
	}
	if err != nil {
		return application.WorkspaceState{}, application.NewError(application.CodeOperationFailed, "read workspace config", err)
	}
	var document workspaceDTO
	decoder := json.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return application.WorkspaceState{}, application.NewError(application.CodeInvalidRequest, "decode workspace config", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values")
		}
		return application.WorkspaceState{}, application.NewError(application.CodeInvalidRequest, "decode workspace config", err)
	}
	if document.Version != workspaceSchemaVersion {
		return application.WorkspaceState{}, application.NewError(application.CodeUnsupportedProtocol,
			fmt.Sprintf("workspace config version %d is unsupported; expected %d", document.Version, workspaceSchemaVersion), nil)
	}
	return document.domain(), nil
}

func (repository *FileRepository) save(state application.WorkspaceState) (resultErr error) {
	temporary, err := os.CreateTemp(filepath.Dir(repository.path), ".workspace-*.json")
	if err != nil {
		return application.NewError(application.CodeOperationFailed, "create staged workspace config", err)
	}
	temporaryPath := temporary.Name()
	defer func() {
		temporary.Close()
		if resultErr != nil {
			os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return application.NewError(application.CodeOperationFailed, "protect staged workspace config", err)
	}
	encoder := json.NewEncoder(temporary)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(dtoFromState(state)); err != nil {
		return application.NewError(application.CodeOperationFailed, "encode workspace config", err)
	}
	if err := temporary.Sync(); err != nil {
		return application.NewError(application.CodeOperationFailed, "sync staged workspace config", err)
	}
	if err := temporary.Close(); err != nil {
		return application.NewError(application.CodeOperationFailed, "close staged workspace config", err)
	}
	if err := os.Rename(temporaryPath, repository.path); err != nil {
		return application.NewError(application.CodeOperationFailed, "replace workspace config", err)
	}
	return nil
}

type workspaceDTO struct {
	Version        int             `json:"version"`
	DeviceID       string          `json:"device_id"`
	NextGeneration uint64          `json:"next_generation"`
	Connections    []connectionDTO `json:"connections"`
}

type connectionDTO struct {
	ID                 string        `json:"id"`
	Label              string        `json:"label"`
	Route              routeDTO      `json:"route"`
	Generation         uint64        `json:"generation"`
	SavedEnvironmentID string        `json:"saved_environment_id"`
	NextRefresh        uint64        `json:"next_refresh"`
	Projection         projectionDTO `json:"projection"`
}

type routeDTO struct {
	Kind        application.AccessKind `json:"kind"`
	Profile     string                 `json:"profile"`
	Host        string                 `json:"host,omitempty"`
	OwnerBinary string                 `json:"owner_binary,omitempty"`
}

type projectionDTO struct {
	RefreshSequence uint64       `json:"refresh_sequence"`
	Owner           ownerDTO     `json:"owner"`
	Sessions        []sessionDTO `json:"sessions"`
	RefreshedAt     time.Time    `json:"refreshed_at"`
	AttemptedAt     time.Time    `json:"attempted_at"`
	Failure         *failureDTO  `json:"failure,omitempty"`
}

type ownerDTO struct {
	InstanceID    string   `json:"instance_id"`
	EnvironmentID string   `json:"environment_id"`
	Revision      int      `json:"revision"`
	Capabilities  []string `json:"capabilities"`
}

type sessionDTO struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Tool          string `json:"tool"`
	Cwd           string `json:"cwd"`
	Group         string `json:"group"`
	Status        string `json:"status"`
	Archived      bool   `json:"archived"`
	InventoryOnly bool   `json:"inventory_only"`
}

type failureDTO struct {
	Code    application.Code `json:"code"`
	Message string           `json:"message"`
}

func dtoFromState(state application.WorkspaceState) workspaceDTO {
	document := workspaceDTO{Version: workspaceSchemaVersion, DeviceID: state.DeviceID, NextGeneration: state.NextGeneration}
	for _, connection := range state.Connections {
		document.Connections = append(document.Connections, connectionDTO{
			ID: connection.ID, Label: connection.Label,
			Route:      routeDTO{Kind: normalizedAccess(connection.Endpoint.Access.Kind), Profile: connection.Endpoint.Profile, Host: connection.Endpoint.Access.Host, OwnerBinary: connection.Endpoint.Access.OwnerBinary},
			Generation: connection.Generation, SavedEnvironmentID: connection.SavedEnvironmentID, NextRefresh: connection.NextRefresh,
			Projection: projectionFromDomain(connection.Projection),
		})
	}
	return document
}

func (document workspaceDTO) domain() application.WorkspaceState {
	state := application.WorkspaceState{DeviceID: document.DeviceID, NextGeneration: document.NextGeneration}
	for _, connection := range document.Connections {
		state.Connections = append(state.Connections, application.Connection{
			ID: connection.ID, Label: connection.Label,
			Endpoint:   application.Endpoint{Profile: connection.Route.Profile, Access: application.Access{Kind: normalizedAccess(connection.Route.Kind), Host: connection.Route.Host, OwnerBinary: connection.Route.OwnerBinary}},
			Generation: connection.Generation, SavedEnvironmentID: connection.SavedEnvironmentID, NextRefresh: connection.NextRefresh,
			Projection: connection.Projection.domain(),
		})
	}
	return state
}

func normalizedAccess(kind application.AccessKind) application.AccessKind {
	if kind == "" {
		return application.AccessLocal
	}
	return kind
}

func projectionFromDomain(projection application.Projection) projectionDTO {
	result := projectionDTO{
		RefreshSequence: projection.RefreshSequence, Owner: ownerFromDomain(projection.Owner),
		RefreshedAt: projection.RefreshedAt, AttemptedAt: projection.AttemptedAt,
	}
	for _, session := range projection.Sessions {
		result.Sessions = append(result.Sessions, sessionFromDomain(session))
	}
	if projection.Failure != nil {
		result.Failure = &failureDTO{Code: projection.Failure.Code, Message: projection.Failure.Message}
	}
	return result
}

func (projection projectionDTO) domain() application.Projection {
	result := application.Projection{
		RefreshSequence: projection.RefreshSequence, Owner: projection.Owner.domain(),
		RefreshedAt: projection.RefreshedAt, AttemptedAt: projection.AttemptedAt,
	}
	for _, session := range projection.Sessions {
		result.Sessions = append(result.Sessions, session.domain())
	}
	if projection.Failure != nil {
		result.Failure = &application.Failure{Code: projection.Failure.Code, Message: projection.Failure.Message}
	}
	return result
}

func ownerFromDomain(owner application.OwnerInfo) ownerDTO {
	return ownerDTO{InstanceID: owner.InstanceID, EnvironmentID: owner.EnvironmentID, Revision: owner.Revision, Capabilities: append([]string(nil), owner.Capabilities...)}
}

func (owner ownerDTO) domain() application.OwnerInfo {
	return application.OwnerInfo{InstanceID: owner.InstanceID, EnvironmentID: owner.EnvironmentID, Revision: owner.Revision, Capabilities: append([]string(nil), owner.Capabilities...)}
}

func sessionFromDomain(session application.Session) sessionDTO {
	return sessionDTO{ID: session.ID, Name: session.Name, Tool: session.Tool, Cwd: session.Cwd, Group: session.Group, Status: session.Status, Archived: session.Archived, InventoryOnly: session.InventoryOnly}
}

func (session sessionDTO) domain() application.Session {
	return application.Session{ID: session.ID, Name: session.Name, Tool: session.Tool, Cwd: session.Cwd, Group: session.Group, Status: session.Status, Archived: session.Archived, InventoryOnly: session.InventoryOnly}
}
