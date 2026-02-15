package settingsguard

import (
	"os"
	"path/filepath"
	"testing"

	"ccgateway/internal/config"
	"ccgateway/internal/scope"
)

func TestCheckEffectiveOverride(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(cwd, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, cwd, ref)

	cfg := config.DefaultForScope(home, cwd, "codex", "default")
	cfg.SettingsLayer = config.SettingsLayerUser

	if err := CheckEffectiveOverride(paths, cfg); err != nil {
		t.Fatalf("expected no override, got %v", err)
	}

	projectSettings := []byte(`{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:1111"}}`)
	if err := os.WriteFile(paths.ClaudeProjectSettingsPath, projectSettings, 0o644); err != nil {
		t.Fatalf("write project settings failed: %v", err)
	}

	if err := CheckEffectiveOverride(paths, cfg); err == nil {
		t.Fatalf("expected override detection error")
	}
}

func TestResolveSettingsPath(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, cwd, ref)

	cfg := config.DefaultForScope(home, cwd, "codex", "default")
	cfg.SettingsLayer = config.SettingsLayerProject
	cfg.SettingsPath = ""
	if got := ResolveSettingsPath(paths, cfg); got != paths.ClaudeProjectSettingsPath {
		t.Fatalf("unexpected path: %s", got)
	}
}
