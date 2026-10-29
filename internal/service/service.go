// Package service installs herdeck to start at login: a launchd agent on
// macOS, a systemd user unit on Linux.
package service

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/template"
)

const Label = "dev.herdeck"

var plistTmpl = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Exe}}</string>
		<string>run</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>Crashed</key>
		<true/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>{{.Path}}</string>
	</dict>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>{{.Log}}</string>
	<key>StandardErrorPath</key>
	<string>{{.Log}}</string>
</dict>
</plist>
`))

var unitTmpl = template.Must(template.New("unit").Parse(`[Unit]
Description=herdeck: herdr status on a Stream Deck Neo
After=graphical-session.target

[Service]
ExecStart={{.Exe}} run --headless
Restart=on-failure
RestartSec=3

[Install]
WantedBy=default.target
`))

type params struct{ Label, Exe, Log, Path string }

// Path is where the service definition lives.
func Path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "LaunchAgents", Label+".plist"), nil
	case "linux":
		return filepath.Join(home, ".config", "systemd", "user", "herdeck.service"), nil
	}
	return "", fmt.Errorf("service install is not supported on %s", runtime.GOOS)
}

// LogPath is where the macOS agent writes its logs.
func LogPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "herdeck.log")
}

func Installed() bool {
	p, err := Path()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

// Write creates the service file for the current executable without loading
// it (it takes effect at next login). Used by the tray toggle, where loading
// would start a second instance.
func Write() (string, error) {
	p, err := Path()
	if err != nil {
		return "", err
	}
	exe, err := Executable()
	if err != nil {
		return "", err
	}
	tmpl := plistTmpl
	if runtime.GOOS == "linux" {
		tmpl = unitTmpl
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, params{Label: Label, Exe: exe, Log: LogPath(), Path: servicePath()}); err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	return p, os.WriteFile(p, buf.Bytes(), 0o644)
}

// Install writes the service file and starts it now.
func Install() (string, error) {
	p, err := Write()
	if err != nil {
		return p, err
	}
	switch runtime.GOOS {
	case "darwin":
		run("launchctl", "bootout", domain()+"/"+Label) // replace a loaded older copy
		return p, run("launchctl", "bootstrap", domain(), p)
	default:
		if err := run("systemctl", "--user", "daemon-reload"); err != nil {
			return p, err
		}
		return p, run("systemctl", "--user", "enable", "--now", "herdeck.service")
	}
}

// Uninstall stops the service and removes its file.
func Uninstall() error {
	p, err := Path()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		run("launchctl", "bootout", domain()+"/"+Label)
	default:
		run("systemctl", "--user", "disable", "--now", "herdeck.service")
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Remove deletes the service file without stopping a running instance.
func Remove() error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Executable returns a path to the running binary that survives upgrades:
// a Homebrew Cellar path (deleted by `brew upgrade`) is mapped to the
// prefix's bin/ symlink.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return stableExe(exe, func(p string) bool { _, err := os.Stat(p); return err == nil }), nil
}

func stableExe(exe string, exists func(string) bool) string {
	if i := strings.Index(exe, "/Cellar/"); i >= 0 {
		if link := filepath.Join(exe[:i], "bin", filepath.Base(exe)); exists(link) {
			return link
		}
	}
	return exe
}

// servicePath is PATH for the launchd agent, which otherwise gets only the
// system directories and can't find a Homebrew-installed herdr.
func servicePath() string {
	return "/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}

func domain() string { return fmt.Sprintf("gui/%d", os.Getuid()) }

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %v: %s", name, args, err, bytes.TrimSpace(out))
	}
	return nil
}
