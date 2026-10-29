package app

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Activator returns a function that brings the terminal running herdr to the
// front (macOS only). setting is focus.activate_app: "" disables, "auto"
// detects the app, anything else is passed to `open -a`.
func Activator(setting string) func(ctx context.Context) error {
	if runtime.GOOS != "darwin" || setting == "" {
		return nil
	}
	if setting != "auto" {
		return func(ctx context.Context) error { return openApp(ctx, setting) }
	}
	var (
		mu     sync.Mutex
		cached string
		at     time.Time
	)
	return func(ctx context.Context) error {
		mu.Lock()
		if cached == "" || time.Since(at) > time.Minute {
			if name, err := detectTerminal(ctx); err == nil {
				cached, at = name, time.Now()
			}
		}
		name := cached
		mu.Unlock()
		if name == "" {
			return errors.New("could not detect the terminal running herdr; set focus.activate_app")
		}
		return openApp(ctx, name)
	}
}

func openApp(ctx context.Context, name string) error {
	return exec.CommandContext(ctx, "open", "-a", name).Run()
}

// detectTerminal walks up from a herdr client process to the first ancestor
// that lives inside an .app bundle, e.g. Terminal.app, Ghostty.app.
func detectTerminal(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "ps", "-axo", "pid=,ppid=,comm=").Output()
	if err != nil {
		return "", err
	}
	return terminalFromPS(string(out))
}

func terminalFromPS(ps string) (string, error) {
	type proc struct {
		ppid int
		comm string
	}
	procs := map[int]proc{}
	var herdrs []int
	for _, line := range strings.Split(ps, "\n") {
		f := strings.Fields(line)
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		ppid, err2 := strconv.Atoi(f[1])
		if err1 != nil || err2 != nil {
			continue
		}
		comm := strings.Join(f[2:], " ")
		procs[pid] = proc{ppid, comm}
		if filepath.Base(comm) == "herdr" {
			herdrs = append(herdrs, pid)
		}
	}
	for _, pid := range herdrs {
		for seen := 0; pid > 1 && seen < 32; seen++ {
			p, ok := procs[pid]
			if !ok {
				break
			}
			if i := strings.Index(p.comm, ".app/Contents/MacOS/"); i >= 0 {
				return filepath.Base(p.comm[:i]), nil
			}
			pid = p.ppid
		}
	}
	return "", errors.New("no terminal app found above a herdr process")
}
