package scope

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestNewRefNormalizes(t *testing.T) {
	ref, err := NewRef("Open_AI", "Work Profile")
	if err != nil {
		t.Fatalf("NewRef failed: %v", err)
	}
	if ref.VendorID != "open-ai" {
		t.Fatalf("unexpected vendor id: %s", ref.VendorID)
	}
	if ref.ProfileID != "work-profile" {
		t.Fatalf("unexpected profile id: %s", ref.ProfileID)
	}
}

func TestBuildPathsIsolation(t *testing.T) {
	home := "/tmp/home"
	cwd := "/tmp/project"
	a := MustRef("codex", "default")
	b := MustRef("codex", "work")
	pa := BuildPaths(home, cwd, a)
	pb := BuildPaths(home, cwd, b)
	if pa.ScopeDir == pb.ScopeDir {
		t.Fatalf("scope dirs must differ")
	}
	if !strings.Contains(pa.ScopeDir, filepath.Join("vendors", "codex", "profiles", "default")) {
		t.Fatalf("unexpected scope dir: %s", pa.ScopeDir)
	}
	if !strings.Contains(pb.ScopeDir, filepath.Join("vendors", "codex", "profiles", "work")) {
		t.Fatalf("unexpected scope dir: %s", pb.ScopeDir)
	}
}
