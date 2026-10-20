package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/whoyoujoshin/aether/jobs"
)

// store keeps texts on disk, one file per sha256, up to maxBytes in all.
type store struct {
	dir      string
	maxBytes int64

	mu   sync.Mutex
	used int64
}

var errFull = errors.New("the job board's storage is full")

func openStore(dir string, maxBytes int64) (*store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &store{dir: dir, maxBytes: maxBytes}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && jobs.ValidHash(e.Name()) {
			s.used += info.Size()
		}
	}
	return s, nil
}

// path is only ever built from a validated hash.
func (s *store) path(hash string) string { return filepath.Join(s.dir, hash) }

func (s *store) get(hash string) ([]byte, error) {
	if !jobs.ValidHash(hash) {
		return nil, os.ErrNotExist
	}
	return os.ReadFile(s.path(hash))
}

func (s *store) has(hash string) bool {
	if !jobs.ValidHash(hash) {
		return false
	}
	_, err := os.Stat(s.path(hash))
	return err == nil
}

// put stores b under its hash; storing it again is a no-op.
func (s *store) put(hash string, b []byte) (created bool, err error) {
	if !jobs.ValidHash(hash) || jobs.Hash(b) != hash {
		return false, fmt.Errorf("the body's sha256 isn't %s", hash)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.path(hash)); err == nil {
		return false, nil
	}
	if s.used+int64(len(b)) > s.maxBytes {
		return false, errFull
	}
	tmp, err := os.CreateTemp(s.dir, ".upload-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if err := os.Rename(tmp.Name(), s.path(hash)); err != nil {
		return false, err
	}
	s.used += int64(len(b))
	return true, nil
}
