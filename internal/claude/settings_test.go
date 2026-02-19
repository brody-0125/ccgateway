package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"ccgateway/internal/state"
)

func TestApplyAndRevertPreservesOriginal(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	snapDir := filepath.Join(dir, "snapshots")
	original := []byte("{\n  \"model\": \"custom\",\n  \"env\": {\n    \"FOO\": \"BAR\"\n  }\n}\n")
	if err := os.WriteFile(settingsPath, original, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	res, err := Apply(settingsPath, snapDir, 18888, "gpt-5.3-codex")
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if res.SnapshotPath == "" || res.SnapshotSHA256 == "" {
		t.Fatalf("snapshot result missing: %#v", res)
	}

	if err := Revert(settingsPath, res.SnapshotPath, res.SnapshotSHA256); err != nil {
		t.Fatalf("revert failed: %v", err)
	}
	restored, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read restored failed: %v", err)
	}
	if state.HashBytes(restored) != state.HashBytes(original) {
		t.Fatalf("original JSON not preserved")
	}
}

func TestApplyNativeCleanupRemovesManagedLocalProxyRouting(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	snapDir := filepath.Join(dir, "snapshots")
	original := []byte("{\n  \"model\": \"gpt-5.3-codex\",\n  \"env\": {\n    \"ANTHROPIC_BASE_URL\": \"http://127.0.0.1:8317\",\n    \"ANTHROPIC_AUTH_TOKEN\": \"ccg::codex::default::gen-1\",\n    \"ANTHROPIC_MODEL\": \"gpt-5.3-codex\",\n    \"ANTHROPIC_SMALL_FAST_MODEL\": \"gpt-5.3-codex\",\n    \"ANTHROPIC_DEFAULT_SONNET_MODEL\": \"gpt-5.3-codex\"\n  }\n}\n")
	if err := os.WriteFile(settingsPath, original, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	if _, err := ApplyWithOptions(ApplyOptions{
		SettingsPath: settingsPath,
		SnapshotDir:  snapDir,
		Port:         8317,
		Model:        "gpt-5.3-codex",
		Mode:         ApplyModeNativeCleanup,
	}); err != nil {
		t.Fatalf("apply native-cleanup failed: %v", err)
	}

	updated, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read updated failed: %v", err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal(updated, &doc); err != nil {
		t.Fatalf("invalid updated json: %v", err)
	}
	if _, ok := doc["model"]; ok {
		t.Fatal("native apply should remove top-level model override")
	}
	env, _ := doc["env"].(map[string]any)
	if _, ok := env["ANTHROPIC_BASE_URL"]; ok {
		t.Fatal("native apply should remove managed local ANTHROPIC_BASE_URL")
	}
	if _, ok := env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatal("native apply should remove managed ANTHROPIC_AUTH_TOKEN")
	}
	if _, ok := env["ANTHROPIC_MODEL"]; ok {
		t.Fatal("native apply should remove ANTHROPIC_MODEL")
	}
	if _, ok := env["ANTHROPIC_SMALL_FAST_MODEL"]; ok {
		t.Fatal("native apply should remove ANTHROPIC_SMALL_FAST_MODEL")
	}
}

func TestApplyNativeDirectSetsModelAndClearsManagedProxyRouting(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	snapDir := filepath.Join(dir, "snapshots")
	original := []byte("{\n  \"env\": {\n    \"ANTHROPIC_BASE_URL\": \"http://127.0.0.1:8317\",\n    \"ANTHROPIC_AUTH_TOKEN\": \"ccg::codex::default::gen-1\",\n    \"CUSTOM\": \"KEEP\"\n  }\n}\n")
	if err := os.WriteFile(settingsPath, original, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	if _, err := ApplyWithOptions(ApplyOptions{
		SettingsPath: settingsPath,
		SnapshotDir:  snapDir,
		Port:         8317,
		Model:        "claude-opus-4-6",
		Mode:         ApplyModeNativeDirect,
	}); err != nil {
		t.Fatalf("apply native-direct failed: %v", err)
	}

	updated, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read updated failed: %v", err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal(updated, &doc); err != nil {
		t.Fatalf("invalid updated json: %v", err)
	}
	if got := doc["model"]; got != "claude-opus-4-6" {
		t.Fatalf("expected top-level model set, got=%v", got)
	}
	env, _ := doc["env"].(map[string]any)
	if _, ok := env["ANTHROPIC_BASE_URL"]; ok {
		t.Fatal("native-direct should remove managed local ANTHROPIC_BASE_URL")
	}
	if _, ok := env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatal("native-direct should remove managed ANTHROPIC_AUTH_TOKEN")
	}
	if got := env["ANTHROPIC_MODEL"]; got != "claude-opus-4-6" {
		t.Fatalf("expected ANTHROPIC_MODEL set, got=%v", got)
	}
	if got := env["CUSTOM"]; got != "KEEP" {
		t.Fatalf("expected custom env preserved, got=%v", got)
	}
}
