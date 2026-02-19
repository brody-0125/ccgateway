package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewManagerReturnsNonNil(t *testing.T) {
	mgr := NewManager()
	if mgr == nil {
		t.Fatal("NewManager() returned nil")
	}
}

func TestUnitPathsUsesLabelsWhenProvided(t *testing.T) {
	homeDir := "/home/testuser"
	defaultProxy := "/fallback/proxy.unit"
	defaultSync := "/fallback/sync.unit"
	proxyLabel := "com.test.proxy"
	syncLabel := "com.test.sync"

	proxyPath, syncPath := UnitPaths(homeDir, defaultProxy, defaultSync, proxyLabel, syncLabel)

	if proxyPath == defaultProxy {
		t.Error("expected proxyPath to differ from default when label is provided")
	}
	if syncPath == defaultSync {
		t.Error("expected syncPath to differ from default when label is provided")
	}
	if !strings.Contains(proxyPath, proxyLabel) {
		t.Errorf("expected proxyPath to contain label %q, got %q", proxyLabel, proxyPath)
	}
	if !strings.Contains(syncPath, syncLabel) {
		t.Errorf("expected syncPath to contain label %q, got %q", syncLabel, syncPath)
	}
}

func TestUnitPathsFallsBackWhenLabelsEmpty(t *testing.T) {
	homeDir := "/home/testuser"
	defaultProxy := "/fallback/proxy.unit"
	defaultSync := "/fallback/sync.unit"

	proxyPath, syncPath := UnitPaths(homeDir, defaultProxy, defaultSync, "", "")

	if proxyPath != defaultProxy {
		t.Errorf("expected proxyPath=%q when label is empty, got %q", defaultProxy, proxyPath)
	}
	if syncPath != defaultSync {
		t.Errorf("expected syncPath=%q when label is empty, got %q", defaultSync, syncPath)
	}
}

func TestUnitPathsTrimsWhitespaceLabels(t *testing.T) {
	homeDir := "/home/testuser"
	defaultProxy := "/fallback/proxy.unit"
	defaultSync := "/fallback/sync.unit"

	proxyPath, syncPath := UnitPaths(homeDir, defaultProxy, defaultSync, "  ", "	")

	if proxyPath != defaultProxy {
		t.Errorf("expected fallback for whitespace-only proxy label, got %q", proxyPath)
	}
	if syncPath != defaultSync {
		t.Errorf("expected fallback for whitespace-only sync label, got %q", syncPath)
	}
}

func TestStatusWithStubSystemctl(t *testing.T) {
	tmp := t.TempDir()
	stubPath := filepath.Join(tmp, "systemctl")

	// Create a stub systemctl that reports units as not-found
	stubScript := "#!/usr/bin/env bash\nif [[ \"$2\" == \"show\" ]]; then\n  echo \"not-found\"\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(stubPath, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("failed to write systemctl stub: %v", err)
	}
	t.Setenv("CCG_SYSTEMCTL_BIN", stubPath)

	mgr := NewManager()
	status, err := mgr.Status("com.test.proxy", "com.test.sync")
	if err != nil {
		t.Fatalf("Status() returned error: %v", err)
	}
	if status.ProxyLoaded {
		t.Error("expected ProxyLoaded=false with not-found stub")
	}
	if status.SyncLoaded {
		t.Error("expected SyncLoaded=false with not-found stub")
	}
}

func TestServiceFilesStructFields(t *testing.T) {
	files := ServiceFiles{
		ProxyLabel:    "proxy",
		SyncLabel:     "sync",
		ProxyUnitPath: "/unit/proxy",
		SyncUnitPath:  "/unit/sync",
		ProxyBinary:   "/bin/proxy",
		ProxyConfig:   "/etc/config",
		ProxyLog:      "/var/log/proxy",
		SyncLog:       "/var/log/sync",
		SyncScript:    "/script/sync.sh",
		AuthSource:    "/auth/source",
		HomeDir:       "/home/user",
	}

	if files.ProxyLabel != "proxy" {
		t.Errorf("unexpected ProxyLabel: %s", files.ProxyLabel)
	}
	if files.HomeDir != "/home/user" {
		t.Errorf("unexpected HomeDir: %s", files.HomeDir)
	}
}
