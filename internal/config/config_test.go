package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaults(t *testing.T) {
	c := Default()
	if c.Layout.Sort != "state" || c.Layout.LongPressMS != 600 || c.Deck.Brightness != 70 || c.Reply.Enabled {
		t.Fatalf("unexpected defaults: %+v", c)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	if _, err := Load(filepath.Join(dir, "missing.toml")); err != nil {
		t.Fatalf("missing file should load defaults: %v", err)
	}

	p := filepath.Join(dir, "c.toml")
	os.WriteFile(p, []byte(`
[layout]
sort = "workspace"
[reply]
enabled = true
[reply.agents.claude]
approve_keys = ["Enter"]
deny_keys = ["Escape"]
`), 0o644)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Layout.Sort != "workspace" || c.Layout.LongPressMS != 600 {
		t.Fatalf("merge failed: %+v", c.Layout)
	}
	if got := c.Reply.Agents["claude"].ApproveKeys; len(got) != 1 || got[0] != "Enter" {
		t.Fatalf("reply agents: %+v", c.Reply)
	}

	os.WriteFile(p, []byte("[layout]\nsortt = \"x\"\n"), 0o644)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "unknown keys") {
		t.Fatalf("want unknown key error, got %v", err)
	}
	os.WriteFile(p, []byte("[layout]\nsort = \"random\"\n"), 0o644)
	if _, err := Load(p); err == nil {
		t.Fatal("want validation error")
	}
}

func TestWriteDefault(t *testing.T) {
	p := filepath.Join(t.TempDir(), "herdeck", "config.toml")
	if err := WriteDefault(p); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != DefaultTOML() {
		t.Fatal("content mismatch")
	}
}
