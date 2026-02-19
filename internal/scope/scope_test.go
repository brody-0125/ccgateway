package scope

import (
	"path/filepath"
	"runtime"
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

func TestBuildPathsPlatformServicePaths(t *testing.T) {
	home := "/tmp/home"
	cwd := "/tmp/project"
	ref := MustRef("codex", "default")
	paths := BuildPaths(home, cwd, ref)

	switch runtime.GOOS {
	case "linux":
		wantDir := filepath.Join(home, ".config", "systemd", "user")
		if paths.ServiceUnitDir != wantDir {
			t.Fatalf("expected ServiceUnitDir=%s, got=%s", wantDir, paths.ServiceUnitDir)
		}
		if !strings.HasSuffix(paths.ProxyUnitPath, ".service") {
			t.Fatalf("expected .service extension on Linux, got=%s", paths.ProxyUnitPath)
		}
		if !strings.HasSuffix(paths.SyncUnitPath, ".service") {
			t.Fatalf("expected .service extension on Linux, got=%s", paths.SyncUnitPath)
		}
	case "darwin":
		wantDir := filepath.Join(home, "Library", "LaunchAgents")
		if paths.ServiceUnitDir != wantDir {
			t.Fatalf("expected ServiceUnitDir=%s, got=%s", wantDir, paths.ServiceUnitDir)
		}
		if !strings.HasSuffix(paths.ProxyUnitPath, ".plist") {
			t.Fatalf("expected .plist extension on macOS, got=%s", paths.ProxyUnitPath)
		}
		if !strings.HasSuffix(paths.SyncUnitPath, ".plist") {
			t.Fatalf("expected .plist extension on macOS, got=%s", paths.SyncUnitPath)
		}
	default:
		t.Skipf("unsupported platform: %s", runtime.GOOS)
	}
}
