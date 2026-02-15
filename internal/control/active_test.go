package control

import (
	"path/filepath"
	"testing"
)

func TestActiveSaveLoadAndLock(t *testing.T) {
	dir := t.TempDir()
	activePath := filepath.Join(dir, "active.json")
	lockPath := filepath.Join(dir, "locks", "switch.lock")

	if err := WithSwitchLock(lockPath, func() error {
		return SaveActive(activePath, ActivePointer{ActiveVendor: "codex", ActiveProfile: "default", ActiveGeneration: "gen-1"})
	}); err != nil {
		t.Fatalf("WithSwitchLock failed: %v", err)
	}

	active, err := LoadActive(activePath)
	if err != nil {
		t.Fatalf("LoadActive failed: %v", err)
	}
	if active.ActiveVendor != "codex" || active.ActiveProfile != "default" {
		t.Fatalf("unexpected active: %#v", active)
	}
}

func TestAllocateScopePort(t *testing.T) {
	dir := t.TempDir()
	portsPath := filepath.Join(dir, "ports.json")
	p, err := AllocateScopePort(portsPath, "codex:default", 18317, nil)
	if err != nil {
		t.Fatalf("AllocateScopePort failed: %v", err)
	}
	if p <= 0 {
		t.Fatalf("invalid port: %d", p)
	}
	p2, err := AllocateScopePort(portsPath, "codex:default", 18317, nil)
	if err != nil {
		t.Fatalf("AllocateScopePort(2) failed: %v", err)
	}
	if p2 != p {
		t.Fatalf("expected stable port assignment, got %d != %d", p2, p)
	}
}
