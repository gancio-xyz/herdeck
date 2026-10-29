package service

import "testing"

func TestStableExe(t *testing.T) {
	exists := func(p string) bool { return p == "/opt/homebrew/bin/herdeck" }
	if got := stableExe("/opt/homebrew/Cellar/herdeck/1.2.3/bin/herdeck", exists); got != "/opt/homebrew/bin/herdeck" {
		t.Fatalf("got %s", got)
	}
	if got := stableExe("/Users/me/go/bin/herdeck", exists); got != "/Users/me/go/bin/herdeck" {
		t.Fatalf("got %s", got)
	}
	if got := stableExe("/usr/local/Cellar/herdeck/1/bin/herdeck", exists); got != "/usr/local/Cellar/herdeck/1/bin/herdeck" {
		t.Fatalf("missing link should keep path, got %s", got)
	}
}
