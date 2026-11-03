package shopper

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// lockMaxAge is how long a crashed shopper's lock is honored. A live
// buy holds it for the whole sign-and-settle, which is shorter than this.
const lockMaxAge = 3 * time.Minute

// acquireLock is an exclusive create, which works on Linux, macOS and
// Windows. A second process does not sign. A lock older than lockMaxAge
// is removed once and tried again.
func acquireLock(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "lock")
	unlock, err := createLock(path)
	if err == nil {
		return unlock, nil
	}
	info, statErr := os.Stat(path)
	if statErr != nil || time.Since(info.ModTime()) < lockMaxAge {
		return nil, fail(CodeRefused, NextRetryLater, "Another shopper is in the middle of a payment. Wait, then run the same command again. Nothing new was signed.")
	}
	_ = os.Remove(path)
	unlock, err = createLock(path)
	if err != nil {
		return nil, fail(CodeRefused, NextRetryLater, "Another shopper is in the middle of a payment. Wait, then run the same command again. Nothing new was signed.")
	}
	return unlock, nil
}

func createLock(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	_ = f.Close()
	return func() { _ = os.Remove(path) }, nil
}
