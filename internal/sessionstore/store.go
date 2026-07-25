package sessionstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Entry maps a bridge session key (e.g. discord:<threadID>) to a pi session file.
type Entry struct {
	SessionFile string    `json:"session_file"`
	CWD         string    `json:"cwd,omitempty"`
	Name        string    `json:"name,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type fileData struct {
	Sessions map[string]Entry `json:"sessions"`
}

// Store is a JSON-backed index of session keys to pi session files.
type Store struct {
	path string
	mu   sync.Mutex
	data fileData
}

// Open loads an existing index or creates an empty one.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("session store path required")
	}
	s := &Store{
		path: path,
		data: fileData{Sessions: make(map[string]Entry)},
	}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if s.data.Sessions == nil {
		s.data.Sessions = make(map[string]Entry)
	}
	return s, nil
}

// Get returns the entry for key, if any.
func (s *Store) Get(key string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data.Sessions[key]
	return e, ok
}

// Set upserts an entry and flushes to disk.
func (s *Store) Set(key string, entry Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry.UpdatedAt = time.Now().UTC()
	s.data.Sessions[key] = entry
	return s.saveLocked()
}

// Delete removes a key and flushes.
func (s *Store) Delete(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data.Sessions, key)
	return s.saveLocked()
}

// Path returns the index file path.
func (s *Store) Path() string { return s.path }

func (s *Store) load() error {
	b, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var data fileData
	if err := json.Unmarshal(b, &data); err != nil {
		return fmt.Errorf("parse session index: %w", err)
	}
	s.data = data
	return nil
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create session index dir: %w", err)
	}
	b, err := json.MarshalIndent(s.data, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
