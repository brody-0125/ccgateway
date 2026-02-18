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
		if paths.LaunchAgentDir != wantDir {
			t.Fatalf("expected LaunchAgentDir=%s, got=%s", wantDir, paths.LaunchAgentDir)
		}
		if !strings.HasSuffix(paths.ProxyPlistPath, ".service") {
			t.Fatalf("expected .service extension on Linux, got=%s", paths.ProxyPlistPath)
		}
		if !strings.HasSuffix(paths.SyncPlistPath, ".service") {
			t.Fatalf("expected .service extension on Linux, got=%s", paths.SyncPlistPath)
		}
	case "darwin":
		wantDir := filepath.Join(home, "Library", "LaunchAgents")
		if paths.LaunchAgentDir != wantDir {
			t.Fatalf("expected LaunchAgentDir=%s, got=%s", wantDir, paths.LaunchAgentDir)
		}
		if !strings.HasSuffix(paths.ProxyPlistPath, ".plist") {
			t.Fatalf("expected .plist extension on macOS, got=%s", paths.ProxyPlistPath)
		}
		if !strings.HasSuffix(paths.SyncPlistPath, ".plist") {
			t.Fatalf("expected .plist extension on macOS, got=%s", paths.SyncPlistPath)
		}
	default:
		t.Skipf("unsupported platform: %s", runtime.GOOS)
	}
}
