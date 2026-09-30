package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/YoanWai/agent-manager/internal/store"
	"golang.org/x/sys/unix"
)

const (
	runtimeDirectoryOverrideEnv = "AGENT_MANAGER_ARCHITECTURE_POC_RUNTIME_DIR"
	environmentIDSetting        = "architecture_poc_environment_id"
	ownerLockName               = ".architecture-poc-owner.lock"
	socketRequestTimeout        = 10 * time.Second
)

func serve(ctx context.Context, dir string, revision int, log io.Writer) error {
	if revision < 1 || revision > 3 {
		return fmt.Errorf("revision must be 1, 2, or 3, got %d", revision)
	}
	profile, err := prepareProfile(dir)
	if err != nil {
		return err
	}
	lock, err := acquireProfileLock(filepath.Join(profile, ownerLockName))
	if err != nil {
		return err
	}
	defer lock.release()

	socket, err := socketPathForProfile(profile)
	if err != nil {
		return err
	}
	if err := removeStaleSocket(socket); err != nil {
		return err
	}

	state, err := store.Open(filepath.Join(profile, "state.db"))
	if err != nil {
		return fmt.Errorf("open profile store: %w", err)
	}
	defer state.Close()
	environmentID, err := ensureEnvironmentID(state)
	if err != nil {
		return err
	}
	instanceID, err := newIdentity("owner")
	if err != nil {
		return err
	}
	owner := &ownerState{
		info: OwnerInfo{
			InstanceID: instanceID, EnvironmentID: environmentID, Revision: revision,
			Capabilities: capabilitiesForRevision(revision),
		},
		store: state,
	}

	address, err := net.ResolveUnixAddr("unix", socket)
	if err != nil {
		return fmt.Errorf("resolve owner socket: %w", err)
	}
	listener, err := net.ListenUnix("unix", address)
	if err != nil {
		return fmt.Errorf("listen on owner socket: %w", err)
	}
	listener.SetUnlinkOnClose(true)
	defer listener.Close()
	if err := os.Chmod(socket, 0o600); err != nil {
		return fmt.Errorf("protect owner socket: %w", err)
	}
	fmt.Fprintf(log, "READY socket=%s environment=%s revision=%d\n", socket, environmentID, revision)

	stopListening := context.AfterFunc(ctx, func() { listener.Close() })
	defer stopListening()
	var handlers sync.WaitGroup
	defer handlers.Wait()
	for {
		connection, err := listener.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept owner request: %w", err)
		}
		handlers.Add(1)
		go func() {
			defer handlers.Done()
			handleConnection(ctx, connection, owner)
		}()
	}
}

func handleConnection(ctx context.Context, connection *net.UnixConn, owner *ownerState) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(socketRequestTimeout))
	stop := context.AfterFunc(ctx, func() { connection.Close() })
	defer stop()

	request, err := decodeRequest(connection)
	var response Response
	if err != nil {
		response = owner.reject("", errorInvalidRequest, err.Error())
	} else {
		response = owner.dispatch(request)
	}
	_ = json.NewEncoder(connection).Encode(response)
}

func ensureEnvironmentID(state *store.Store) (string, error) {
	value, err := state.Setting(environmentIDSetting)
	if err != nil {
		return "", fmt.Errorf("read environment identity: %w", err)
	}
	if value != "" {
		return value, nil
	}
	value, err = newIdentity("env")
	if err != nil {
		return "", err
	}
	if err := state.SetSetting(environmentIDSetting, value); err != nil {
		return "", fmt.Errorf("store environment identity: %w", err)
	}
	return value, nil
}

func newIdentity(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate %s identity: %w", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(value), nil
}

func prepareProfile(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("profile directory is required")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve profile directory: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return "", fmt.Errorf("create profile directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve profile directory links: %w", err)
	}
	return resolved, nil
}

func canonicalProfile(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("profile directory is required")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve profile directory: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("resolve profile directory links: %w", err)
	}
	return resolved, nil
}

func socketPathForProfile(dir string) (string, error) {
	profile, err := canonicalProfile(dir)
	if err != nil {
		return "", err
	}
	runtimeDir, err := secureRuntimeDirectory()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(profile))
	return filepath.Join(runtimeDir, "profile-"+hex.EncodeToString(digest[:8])+".sock"), nil
}

func secureRuntimeDirectory() (string, error) {
	dir := os.Getenv(runtimeDirectoryOverrideEnv)
	if dir == "" {
		dir = filepath.Join("/tmp", fmt.Sprintf("agent-manager-architecture-poc-%d", os.Getuid()))
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", fmt.Errorf("create socket runtime directory: %w", err)
	}
	var stat unix.Stat_t
	if err := unix.Lstat(dir, &stat); err != nil {
		return "", fmt.Errorf("inspect socket runtime directory: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return "", fmt.Errorf("socket runtime path %q is not a directory", dir)
	}
	if stat.Uid != uint32(os.Getuid()) {
		return "", fmt.Errorf("socket runtime directory %q is not owned by uid %d", dir, os.Getuid())
	}
	if stat.Mode&0o777 != 0o700 {
		return "", fmt.Errorf("socket runtime directory %q must have mode 0700", dir)
	}
	return dir, nil
}

func removeStaleSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect prior owner socket: %w", err)
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refuse to replace non-socket path %q", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove stale owner socket: %w", err)
	}
	return nil
}

type profileLock struct {
	file *os.File
}

func acquireProfileLock(path string) (*profileLock, error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open profile owner lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("%s: profile %q already has an owner", errorOwnerAlreadyRunning, filepath.Dir(path))
		}
		return nil, fmt.Errorf("lock profile owner: %w", err)
	}
	return &profileLock{file: file}, nil
}

func (lock *profileLock) release() {
	_ = unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	_ = lock.file.Close()
}
