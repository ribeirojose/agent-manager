package sessioncmd

import (
	"errors"
	"sync"
	"time"

	"github.com/YoanWai/agent-manager/internal/config"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/tmux"
)

type ArchiveRequest struct {
	CallerID string
	TargetID string
	Archived bool
	Words    Vocabulary
}

type ArchiveOwner interface {
	Archive(ArchiveRequest) (Session, error)
}

type InboxMaintenance interface {
	MaintainInbox() error
}

func NewSessionsWithArchiveOwner(configDir string, words Vocabulary, owner ArchiveOwner) *Sessions {
	if owner == nil {
		panic("archive owner is required for explicit composition")
	}
	sessions := NewSessions(configDir, words)
	sessions.archiveOwner = owner
	return sessions
}

type SessionOwner struct {
	mu      sync.Mutex
	runtime runtime
}

func NewSessionOwner(cfg config.Config, st *store.Store, driver *tmux.Driver) *SessionOwner {
	return &SessionOwner{runtime: runtime{cfg: cfg, store: st, driver: driver}}
}

func (owner *SessionOwner) Archive(request ArchiveRequest) (Session, error) {
	owner.mu.Lock()
	defer owner.mu.Unlock()
	runtime := owner.runtime
	runtime.words = request.Words
	if _, err := runtime.caller(request.CallerID); err != nil {
		return Session{}, err
	}
	target, err := runtime.agent(request.TargetID)
	if err != nil {
		return Session{}, err
	}
	if target.ID == request.CallerID && request.Archived {
		return Session{}, errors.New("a session cannot archive itself")
	}
	running := runtime.driver.Exists(target.ID)
	if request.Archived && running {
		if pane, err := runtime.driver.CapturePane(target.ID); err == nil && pane != "" {
			if err := runtime.store.SetSnapshot(target.ID, pane); err != nil {
				return Session{}, err
			}
		}
	}
	if err := runtime.store.SetArchived(target.ID, request.Archived); err != nil {
		return Session{}, err
	}
	target.Archived = request.Archived
	return runtime.sessionInfo(target, running, false), nil
}

type InboxOwner struct {
	store *store.Store
	now   func() time.Time
}

func NewInboxOwner(st *store.Store) *InboxOwner {
	return &InboxOwner{store: st, now: time.Now}
}

func (owner *InboxOwner) MaintainInbox() error {
	return owner.store.PruneInbox(owner.now().Add(-24 * time.Hour))
}
