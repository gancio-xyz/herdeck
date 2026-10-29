package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// Lock is a single-instance lock: two herdeck processes would fight over the
// deck (which allows only one client).
type Lock struct{ f *os.File }

// LockPath is fixed (not $TMPDIR, which differs between a shell and launchd)
// so every instance of the same user sees the same lock.
func LockPath() string {
	return filepath.Join("/tmp", fmt.Sprintf("herdeck-%d.lock", os.Getuid()))
}

// ErrRunning means another herdeck already holds the lock.
var ErrRunning = errors.New("herdeck is already running")

// AcquireLock takes the lock or returns an error wrapping ErrRunning.
func AcquireLock() (*Lock, error) {
	f, err := os.OpenFile(LockPath(), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if pid := LockHolder(); pid > 0 {
			return nil, fmt.Errorf("%w (pid %d)", ErrRunning, pid)
		}
		return nil, ErrRunning
	}
	f.Truncate(0)
	f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	return &Lock{f}, nil
}

// LockHolder returns the pid of a running herdeck, or 0.
func LockHolder() int {
	f, err := os.Open(LockPath())
	if err != nil {
		return 0
	}
	defer f.Close()
	if syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB) == nil {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		return 0 // nobody holds it
	}
	b, _ := os.ReadFile(LockPath())
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	return pid
}

func (l *Lock) Release() {
	syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
}
