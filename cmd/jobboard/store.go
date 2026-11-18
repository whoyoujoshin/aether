package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/whoyoujoshin/aether/jobs"
)

// store keeps texts on disk, one file per sha256, up to maxBytes in all.
// When a new text wouldn't fit it makes room by deleting others: first
// those its caller says can go (texts of withdrawn jobs, or no job's),
// then the oldest. So the store never stays full, and a text that was
// deleted can be uploaded again, since its memo still names it.
type store struct {
	dir      string
	maxBytes int64
	// onEvict is told of each text deleted to make room.
	onEvict func(hash string)

	mu    sync.Mutex
	used  int64
	files map[string]storedFile
}

type storedFile struct {
	size int64
	at   time.Time
}

// errFull means a text is bigger than the whole store.
var errFull = errors.New("the text is larger than the job board's whole storage")

func openStore(dir string, maxBytes int64) (*store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &store{dir: dir, maxBytes: maxBytes, files: map[string]storedFile{}}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && jobs.ValidHash(e.Name()) {
			s.used += info.Size()
			s.files[e.Name()] = storedFile{size: info.Size(), at: info.ModTime()}
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

// put stores b under its hash; storing it again is a no-op. If b doesn't
// fit, it deletes texts to make room: those goesFirst says yes to, oldest
// first, then the rest, oldest first. goesFirst may be nil.
func (s *store) put(hash string, b []byte, goesFirst func(hash string) bool) (created bool, err error) {
	if !jobs.ValidHash(hash) || jobs.Hash(b) != hash {
		return false, fmt.Errorf("the body's sha256 isn't %s", hash)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.path(hash)); err == nil {
		return false, nil
	}
	if int64(len(b)) > s.maxBytes {
		return false, errFull
	}
	if s.used+int64(len(b)) > s.maxBytes {
		if err := s.makeRoomLocked(s.used+int64(len(b))-s.maxBytes, goesFirst); err != nil {
			return false, err
		}
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
	s.files[hash] = storedFile{size: int64(len(b)), at: time.Now()}
	return true, nil
}

// makeRoomLocked deletes texts until at least need bytes are free.
func (s *store) makeRoomLocked(need int64, goesFirst func(string) bool) error {
	type candidate struct {
		hash  string
		first bool
		storedFile
	}
	var cands []candidate
	for h, f := range s.files {
		cands = append(cands, candidate{h, goesFirst != nil && goesFirst(h), f})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].first != cands[j].first {
			return cands[i].first
		}
		if !cands[i].at.Equal(cands[j].at) {
			return cands[i].at.Before(cands[j].at)
		}
		return cands[i].hash < cands[j].hash
	})
	freed := int64(0)
	for _, c := range cands {
		if freed >= need {
			break
		}
		if err := os.Remove(s.path(c.hash)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		delete(s.files, c.hash)
		s.used -= c.size
		freed += c.size
		if s.onEvict != nil {
			s.onEvict(c.hash)
		}
	}
	if freed < need {
		return errFull
	}
	return nil
}
