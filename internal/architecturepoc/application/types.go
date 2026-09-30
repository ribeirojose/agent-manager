package application

import (
	"sort"
	"strings"
	"time"
)

const (
	CapabilitySessionsRead       = "sessions.read"
	CapabilityFixtureCreateBound = "fixture_session.create_bound"
	CapabilityFixtureRename      = "fixture_session.rename"
)

type Scope string

const (
	ScopeDevice    Scope = "device"
	ScopeWorkspace Scope = "workspace"
)

type TargetKind string

const (
	TargetDevice     TargetKind = "device"
	TargetConnection TargetKind = "connection"
)

type AccessKind string

const (
	AccessLocal AccessKind = "local"
	AccessSSH   AccessKind = "ssh"
)

type Access struct {
	Kind        AccessKind
	Host        string
	OwnerBinary string
}

type Endpoint struct {
	Profile string
	Access  Access
}

type OwnerTarget struct {
	Kind         TargetKind
	ConnectionID string
}

func DeviceTarget() OwnerTarget { return OwnerTarget{Kind: TargetDevice} }

func ConnectionTarget(id string) OwnerTarget {
	return OwnerTarget{Kind: TargetConnection, ConnectionID: id}
}

type SessionTarget struct {
	Owner     OwnerTarget
	SessionID string
}

type OwnerInfo struct {
	InstanceID    string
	EnvironmentID string
	Revision      int
	Capabilities  []string
}

func (info OwnerInfo) HasCapability(capability string) bool {
	for _, current := range info.Capabilities {
		if current == capability {
			return true
		}
	}
	return false
}

type ReaderToken struct {
	Target                OwnerTarget
	Generation            uint64
	RefreshSequence       uint64
	EnvironmentID         string
	OwnerInstanceID       string
	Revision              int
	CanCreateBoundFixture bool
	CanRenameFixture      bool
}

func tokenFor(target OwnerTarget, generation, sequence uint64, owner OwnerInfo) ReaderToken {
	return ReaderToken{
		Target: target, Generation: generation, RefreshSequence: sequence,
		EnvironmentID: owner.EnvironmentID, OwnerInstanceID: owner.InstanceID, Revision: owner.Revision,
		CanCreateBoundFixture: owner.HasCapability(CapabilityFixtureCreateBound),
		CanRenameFixture:      owner.HasCapability(CapabilityFixtureRename),
	}
}

type Session struct {
	ID            string
	Name          string
	Tool          string
	Cwd           string
	Group         string
	Status        string
	Archived      bool
	InventoryOnly bool
}

type FixtureDraft struct {
	ID    string
	Name  string
	Tool  string
	Cwd   string
	Group string
}

type MutationGuard struct {
	ExpectedEnvironmentID   string
	ExpectedOwnerInstanceID string
}

type OwnerInventory struct {
	Owner    OwnerInfo
	Sessions []Session
}

type OwnerMutation struct {
	Owner   OwnerInfo
	Session Session
	Notice  string
}

type MutationResult struct {
	Target  SessionTarget
	Token   ReaderToken
	Session Session
	Notice  string
}

type Failure struct {
	Code    Code
	Message string
}

type FreshnessState string

const (
	FreshnessUnread FreshnessState = "unread"
	FreshnessFresh  FreshnessState = "fresh"
	FreshnessStale  FreshnessState = "stale"
)

type Freshness struct {
	State       FreshnessState
	RefreshedAt time.Time
	AttemptedAt time.Time
	Failure     *Failure
}

type SessionProjection struct {
	Target  SessionTarget
	Token   ReaderToken
	Session Session
}

type TargetProjection struct {
	Target    OwnerTarget
	Label     string
	Token     ReaderToken
	Owner     OwnerInfo
	Freshness Freshness
	Sessions  []SessionProjection
}

type Inventory struct {
	Scope   Scope
	Targets []TargetProjection
}

type Projection struct {
	RefreshSequence uint64
	Owner           OwnerInfo
	Sessions        []Session
	RefreshedAt     time.Time
	AttemptedAt     time.Time
	Failure         *Failure
}

type Connection struct {
	ID                 string
	Label              string
	Endpoint           Endpoint
	Generation         uint64
	SavedEnvironmentID string
	NextRefresh        uint64
	Projection         Projection
}

type NewConnection struct {
	ID       string
	Label    string
	Endpoint Endpoint
}

type WorkspaceState struct {
	DeviceID       string
	NextGeneration uint64
	Connections    []Connection
}

func cloneOwner(owner OwnerInfo) OwnerInfo {
	owner.Capabilities = append([]string(nil), owner.Capabilities...)
	sort.Strings(owner.Capabilities)
	return owner
}

func cloneState(state WorkspaceState) WorkspaceState {
	state.Connections = append([]Connection(nil), state.Connections...)
	for index := range state.Connections {
		projection := &state.Connections[index].Projection
		projection.Owner = cloneOwner(projection.Owner)
		projection.Sessions = append([]Session(nil), projection.Sessions...)
		if projection.Failure != nil {
			copy := *projection.Failure
			projection.Failure = &copy
		}
	}
	return state
}

func normalized(value string) string { return strings.TrimSpace(value) }
