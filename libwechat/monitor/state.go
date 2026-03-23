package monitor

import (
	"errors"
	"io/fs"
	"sync"

	"github.com/CMGS/gua/libwechat/utils/jsonfile"
)

// SyncState persists the long-poll sync buffer between restarts.
type SyncState interface {
	Load() (string, error)
	Save(buf string) error
}

type memorySyncState struct {
	mu  sync.Mutex
	buf string
}

// NewMemorySyncState returns an in-memory SyncState with no persistence.
func NewMemorySyncState() SyncState {
	return &memorySyncState{}
}

func (m *memorySyncState) Load() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.buf, nil
}

func (m *memorySyncState) Save(buf string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.buf = buf
	return nil
}

type fileSyncState struct {
	mu      sync.Mutex
	path    string
	lastBuf string
}

type fileSyncData struct {
	GetUpdatesBuf string `json:"get_updates_buf"`
}

// NewFileSyncState returns a SyncState that persists to a JSON file.
func NewFileSyncState(path string) SyncState {
	return &fileSyncState{path: path}
}

func (f *fileSyncState) Load() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	state, err := jsonfile.Read[fileSyncData](f.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	f.lastBuf = state.GetUpdatesBuf
	return state.GetUpdatesBuf, nil
}

func (f *fileSyncState) Save(buf string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if buf == f.lastBuf {
		return nil
	}

	state := fileSyncData{GetUpdatesBuf: buf}
	if err := jsonfile.Write(f.path, &state, 0o600); err != nil {
		return err
	}
	f.lastBuf = buf
	return nil
}
