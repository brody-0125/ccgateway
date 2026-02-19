package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestSmartRevertPreservesUserChanges(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	snapDir := filepath.Join(dir, "snapshots")
	original := []byte("{\n  \"custom\": \"a\",\n  \"env\": {\n    \"MY_VAR\": \"x\"\n  }\n}\n")
	if err := os.WriteFile(settingsPath, original, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	res, err := Apply(settingsPath, snapDir, 18888, "gpt-5.3-codex")
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Simulate user editing settings after Apply.
	applied, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read applied failed: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(applied, &doc); err != nil {
		t.Fatalf("parse applied failed: %v", err)
	}
	doc["custom"] = "b"
	doc["new_key"] = "new"
	env := doc["env"].(map[string]any)
	env["MY_VAR"] = "y"
	edited, _ := json.MarshalIndent(doc, "", "  ")
	edited = append(edited, '\n')
	if err := os.WriteFile(settingsPath, edited, 0o600); err != nil {
		t.Fatalf("write user edits failed: %v", err)
	}

	if err := SmartRevert(settingsPath, res.SnapshotPath, res.SnapshotSHA256); err != nil {
		t.Fatalf("smart revert failed: %v", err)
	}

	restored, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read restored failed: %v", err)
	}
	var result map[string]any
	if err := json.Unmarshal(restored, &result); err != nil {
		t.Fatalf("parse restored failed: %v", err)
	}

	// User changes preserved.
	if result["custom"] != "b" {
		t.Fatalf("expected custom='b', got=%v", result["custom"])
	}
	if result["new_key"] != "new" {
		t.Fatalf("expected new_key='new', got=%v", result["new_key"])
	}
	resEnv, _ := result["env"].(map[string]any)
	if resEnv["MY_VAR"] != "y" {
		t.Fatalf("expected MY_VAR='y', got=%v", resEnv["MY_VAR"])
	}

	// Managed keys removed (were not in original).
	if _, ok := result["model"]; ok {
		t.Fatal("expected model removed after smart revert")
	}
	for _, key := range ManagedEnvKeys() {
		if _, ok := resEnv[key]; ok {
			t.Fatalf("expected managed env key %s removed after smart revert", key)
		}
	}
}

func TestSmartRevertRestoresOriginalManagedKeys(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	snapDir := filepath.Join(dir, "snapshots")
	original := []byte("{\n  \"model\": \"user-custom\",\n  \"env\": {\n    \"ANTHROPIC_BASE_URL\": \"https://custom.api.com\"\n  }\n}\n")
	if err := os.WriteFile(settingsPath, original, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	res, err := Apply(settingsPath, snapDir, 18888, "gpt-5.3-codex")
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// User adds a non-managed key after Apply.
	applied, _ := os.ReadFile(settingsPath)
	var doc map[string]any
	json.Unmarshal(applied, &doc)
	doc["theme"] = "dark"
	edited, _ := json.MarshalIndent(doc, "", "  ")
	edited = append(edited, '\n')
	os.WriteFile(settingsPath, edited, 0o600)

	if err := SmartRevert(settingsPath, res.SnapshotPath, res.SnapshotSHA256); err != nil {
		t.Fatalf("smart revert failed: %v", err)
	}

	restored, _ := os.ReadFile(settingsPath)
	var result map[string]any
	json.Unmarshal(restored, &result)

	// Original managed key values restored.
	if result["model"] != "user-custom" {
		t.Fatalf("expected model='user-custom', got=%v", result["model"])
	}
	resEnv, _ := result["env"].(map[string]any)
	if resEnv["ANTHROPIC_BASE_URL"] != "https://custom.api.com" {
		t.Fatalf("expected ANTHROPIC_BASE_URL restored to original, got=%v", resEnv["ANTHROPIC_BASE_URL"])
	}
	// User addition preserved.
	if result["theme"] != "dark" {
		t.Fatalf("expected theme='dark' preserved, got=%v", result["theme"])
	}
	// Gateway-added keys that weren't in original removed.
	if _, ok := resEnv["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatal("expected ANTHROPIC_AUTH_TOKEN removed")
	}
}

func TestSmartRevertFallsBackOnCorruptFile(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	snapDir := filepath.Join(dir, "snapshots")
	original := []byte("{\n  \"env\": {\n    \"FOO\": \"BAR\"\n  }\n}\n")
	if err := os.WriteFile(settingsPath, original, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	res, err := Apply(settingsPath, snapDir, 18888, "gpt-5.3-codex")
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Corrupt settings.json with invalid JSON.
	if err := os.WriteFile(settingsPath, []byte("{corrupt json!!!"), 0o600); err != nil {
		t.Fatalf("write corrupt failed: %v", err)
	}

	// SmartRevert should fall back to full Revert.
	if err := SmartRevert(settingsPath, res.SnapshotPath, res.SnapshotSHA256); err != nil {
		t.Fatalf("smart revert fallback failed: %v", err)
	}

	restored, _ := os.ReadFile(settingsPath)
	if state.HashBytes(restored) != state.HashBytes(original) {
		t.Fatalf("expected full restore on corrupt file, got:\n%s", string(restored))
	}
}

func TestSmartRevertRemovesEmptyEnv(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	snapDir := filepath.Join(dir, "snapshots")
	// Original has no env key.
	original := []byte("{\n  \"custom\": \"value\"\n}\n")
	if err := os.WriteFile(settingsPath, original, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	res, err := Apply(settingsPath, snapDir, 18888, "gpt-5.3-codex")
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	if err := SmartRevert(settingsPath, res.SnapshotPath, res.SnapshotSHA256); err != nil {
		t.Fatalf("smart revert failed: %v", err)
	}

	restored, _ := os.ReadFile(settingsPath)
	var result map[string]any
	if err := json.Unmarshal(restored, &result); err != nil {
		t.Fatalf("parse restored failed: %v", err)
	}
	if _, ok := result["env"]; ok {
		t.Fatal("expected env key removed when original had no env")
	}
	if result["custom"] != "value" {
		t.Fatalf("expected custom='value' preserved, got=%v", result["custom"])
	}
}

func TestSmartRevertAfterNativeCleanupApply(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	snapDir := filepath.Join(dir, "snapshots")
	original := []byte("{\n  \"model\": \"gpt-5.3-codex\",\n  \"env\": {\n    \"ANTHROPIC_BASE_URL\": \"http://127.0.0.1:8317\",\n    \"ANTHROPIC_AUTH_TOKEN\": \"ccb::codex::default::gen-1\",\n    \"ANTHROPIC_MODEL\": \"gpt-5.3-codex\",\n    \"MY_VAR\": \"keep\"\n  }\n}\n")
	if err := os.WriteFile(settingsPath, original, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	res, err := ApplyWithOptions(ApplyOptions{
		SettingsPath: settingsPath,
		SnapshotDir:  snapDir,
		Port:         8317,
		Model:        "gpt-5.3-codex",
		Mode:         ApplyModeNativeCleanup,
	})
	if err != nil {
		t.Fatalf("apply native-cleanup failed: %v", err)
	}

	// User adds a key after cleanup Apply.
	applied, _ := os.ReadFile(settingsPath)
	var doc map[string]any
	json.Unmarshal(applied, &doc)
	doc["user_added"] = "yes"
	edited, _ := json.MarshalIndent(doc, "", "  ")
	edited = append(edited, '\n')
	os.WriteFile(settingsPath, edited, 0o600)

	if err := SmartRevert(settingsPath, res.SnapshotPath, res.SnapshotSHA256); err != nil {
		t.Fatalf("smart revert failed: %v", err)
	}

	restored, _ := os.ReadFile(settingsPath)
	var result map[string]any
	json.Unmarshal(restored, &result)

	// Original managed keys restored from snapshot.
	if result["model"] != "gpt-5.3-codex" {
		t.Fatalf("expected model restored, got=%v", result["model"])
	}
	resEnv, _ := result["env"].(map[string]any)
	if resEnv["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8317" {
		t.Fatalf("expected ANTHROPIC_BASE_URL restored, got=%v", resEnv["ANTHROPIC_BASE_URL"])
	}
	if resEnv["ANTHROPIC_AUTH_TOKEN"] != "ccb::codex::default::gen-1" {
		t.Fatalf("expected ANTHROPIC_AUTH_TOKEN restored, got=%v", resEnv["ANTHROPIC_AUTH_TOKEN"])
	}
	if resEnv["ANTHROPIC_MODEL"] != "gpt-5.3-codex" {
		t.Fatalf("expected ANTHROPIC_MODEL restored, got=%v", resEnv["ANTHROPIC_MODEL"])
	}
	// Non-managed keys preserved.
	if resEnv["MY_VAR"] != "keep" {
		t.Fatalf("expected MY_VAR='keep', got=%v", resEnv["MY_VAR"])
	}
	if result["user_added"] != "yes" {
		t.Fatalf("expected user_added='yes' preserved, got=%v", result["user_added"])
	}
}

func TestSmartRevertAfterNativeDirectApply(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	snapDir := filepath.Join(dir, "snapshots")
	original := []byte("{\n  \"env\": {\n    \"ANTHROPIC_BASE_URL\": \"http://127.0.0.1:8317\",\n    \"ANTHROPIC_AUTH_TOKEN\": \"ccb::codex::default::gen-1\",\n    \"CUSTOM\": \"val\"\n  }\n}\n")
	if err := os.WriteFile(settingsPath, original, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	res, err := ApplyWithOptions(ApplyOptions{
		SettingsPath: settingsPath,
		SnapshotDir:  snapDir,
		Port:         8317,
		Model:        "claude-opus-4-6",
		Mode:         ApplyModeNativeDirect,
	})
	if err != nil {
		t.Fatalf("apply native-direct failed: %v", err)
	}

	// User adds a key.
	applied, _ := os.ReadFile(settingsPath)
	var doc map[string]any
	json.Unmarshal(applied, &doc)
	doc["theme"] = "light"
	edited, _ := json.MarshalIndent(doc, "", "  ")
	edited = append(edited, '\n')
	os.WriteFile(settingsPath, edited, 0o600)

	if err := SmartRevert(settingsPath, res.SnapshotPath, res.SnapshotSHA256); err != nil {
		t.Fatalf("smart revert failed: %v", err)
	}

	restored, _ := os.ReadFile(settingsPath)
	var result map[string]any
	json.Unmarshal(restored, &result)

	// Original had no model key.
	if _, ok := result["model"]; ok {
		t.Fatal("expected model removed (was not in original)")
	}
	resEnv, _ := result["env"].(map[string]any)
	// Original proxy keys restored.
	if resEnv["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:8317" {
		t.Fatalf("expected ANTHROPIC_BASE_URL restored, got=%v", resEnv["ANTHROPIC_BASE_URL"])
	}
	if resEnv["ANTHROPIC_AUTH_TOKEN"] != "ccb::codex::default::gen-1" {
		t.Fatalf("expected ANTHROPIC_AUTH_TOKEN restored, got=%v", resEnv["ANTHROPIC_AUTH_TOKEN"])
	}
	// Model keys not in original -> removed.
	if _, ok := resEnv["ANTHROPIC_MODEL"]; ok {
		t.Fatal("expected ANTHROPIC_MODEL removed (not in original)")
	}
	// User changes preserved.
	if resEnv["CUSTOM"] != "val" {
		t.Fatalf("expected CUSTOM='val', got=%v", resEnv["CUSTOM"])
	}
	if result["theme"] != "light" {
		t.Fatalf("expected theme='light' preserved, got=%v", result["theme"])
	}
}

func TestSmartRevertWhenSettingsFileDeleted(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	snapDir := filepath.Join(dir, "snapshots")
	original := []byte("{\n  \"env\": {\n    \"FOO\": \"BAR\"\n  }\n}\n")
	if err := os.WriteFile(settingsPath, original, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	res, err := Apply(settingsPath, snapDir, 18888, "gpt-5.3-codex")
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Delete settings file.
	os.Remove(settingsPath)

	// SmartRevert should fall back to full Revert (file recreated from snapshot).
	if err := SmartRevert(settingsPath, res.SnapshotPath, res.SnapshotSHA256); err != nil {
		t.Fatalf("smart revert fallback on deleted file failed: %v", err)
	}

	restored, _ := os.ReadFile(settingsPath)
	if state.HashBytes(restored) != state.HashBytes(original) {
		t.Fatalf("expected full restore when file deleted, got:\n%s", string(restored))
	}
}

func TestSmartRevertWithCorruptSnapshot(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	snapDir := filepath.Join(dir, "snapshots")
	original := []byte("{\n  \"env\": {\n    \"FOO\": \"BAR\"\n  }\n}\n")
	if err := os.WriteFile(settingsPath, original, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	res, err := Apply(settingsPath, snapDir, 18888, "gpt-5.3-codex")
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Corrupt snapshot file (valid bytes but different content → hash mismatch).
	if err := os.WriteFile(res.SnapshotPath, []byte("{\"corrupt\": true}\n"), 0o600); err != nil {
		t.Fatalf("write corrupt snapshot failed: %v", err)
	}

	err = SmartRevert(settingsPath, res.SnapshotPath, res.SnapshotSHA256)
	if err == nil {
		t.Fatal("expected error from corrupt snapshot, got nil")
	}
	if !strings.Contains(err.Error(), "snapshot hash mismatch") {
		t.Fatalf("expected hash mismatch error, got: %v", err)
	}
}

func TestSmartRevertWhenUserRemovesEnv(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")
	snapDir := filepath.Join(dir, "snapshots")
	original := []byte("{\n  \"env\": {\n    \"MY_VAR\": \"x\"\n  }\n}\n")
	if err := os.WriteFile(settingsPath, original, 0o600); err != nil {
		t.Fatalf("write failed: %v", err)
	}

	res, err := Apply(settingsPath, snapDir, 18888, "gpt-5.3-codex")
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// User deletes entire env key after Apply.
	os.WriteFile(settingsPath, []byte("{\n  \"custom\": \"val\"\n}\n"), 0o600)

	if err := SmartRevert(settingsPath, res.SnapshotPath, res.SnapshotSHA256); err != nil {
		t.Fatalf("smart revert failed: %v", err)
	}

	restored, _ := os.ReadFile(settingsPath)
	var result map[string]any
	json.Unmarshal(restored, &result)

	// User's custom key preserved.
	if result["custom"] != "val" {
		t.Fatalf("expected custom='val', got=%v", result["custom"])
	}
	// SmartRevert only restores managed keys from snapshot; MY_VAR is
	// non-managed, so it stays absent after the user deleted env.
	resEnv, _ := result["env"].(map[string]any)
	if _, ok := resEnv["MY_VAR"]; ok {
		t.Fatalf("expected MY_VAR absent (user deleted env, non-managed key), got=%v", resEnv["MY_VAR"])
	}
	// Managed keys not in original should not appear.
	for _, key := range ManagedEnvKeys() {
		if _, ok := resEnv[key]; ok {
			t.Fatalf("expected managed env key %s absent, got=%v", key, resEnv[key])
		}
	}
}

func TestCollectSnapshotsRemovesOldFiles(t *testing.T) {
	dir := t.TempDir()

	// Create 8 snapshot files with staggered mtime.
	var paths []string
	for i := 0; i < 8; i++ {
		p := filepath.Join(dir, fmt.Sprintf("claude-settings-%d.json", i))
		if err := os.WriteFile(p, []byte("{}"), 0o600); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		// Set mtime so file 0 is oldest, file 7 is newest.
		ts := time.Now().Add(time.Duration(i-8) * time.Minute)
		os.Chtimes(p, ts, ts)
		paths = append(paths, p)
	}

	res := CollectSnapshots(dir, 5, nil)
	if len(res.Removed) != 3 {
		t.Fatalf("expected 3 removed, got %d (removed=%v kept=%v)", len(res.Removed), res.Removed, res.Kept)
	}
	if len(res.Kept) != 5 {
		t.Fatalf("expected 5 kept, got %d", len(res.Kept))
	}
	for _, e := range res.Errors {
		t.Errorf("unexpected error: %v", e)
	}

	// The 3 oldest files should be gone.
	for i := 0; i < 3; i++ {
		if _, err := os.Stat(paths[i]); !os.IsNotExist(err) {
			t.Errorf("expected %s removed, still exists", filepath.Base(paths[i]))
		}
	}
	// The 5 newest should remain.
	for i := 3; i < 8; i++ {
		if _, err := os.Stat(paths[i]); err != nil {
			t.Errorf("expected %s kept, got err: %v", filepath.Base(paths[i]), err)
		}
	}
}

func TestCollectSnapshotsProtectsReferencedFile(t *testing.T) {
	dir := t.TempDir()

	// Create 4 snapshot files: oldest one is protected.
	var paths []string
	for i := 0; i < 4; i++ {
		p := filepath.Join(dir, fmt.Sprintf("snap-%d.json", i))
		os.WriteFile(p, []byte("{}"), 0o600)
		ts := time.Now().Add(time.Duration(i-4) * time.Minute)
		os.Chtimes(p, ts, ts)
		paths = append(paths, p)
	}

	// Retain 2, but protect the oldest (paths[0]).
	protected := map[string]bool{paths[0]: true}
	res := CollectSnapshots(dir, 2, protected)

	// paths[0] is oldest but protected → kept.
	// paths[1] is second-oldest, not protected, over retain → removed.
	if len(res.Removed) != 1 {
		t.Fatalf("expected 1 removed, got %d (removed=%v)", len(res.Removed), res.Removed)
	}
	if res.Removed[0] != paths[1] {
		t.Fatalf("expected %s removed, got %s", filepath.Base(paths[1]), filepath.Base(res.Removed[0]))
	}
	if _, err := os.Stat(paths[0]); err != nil {
		t.Fatalf("protected file should still exist: %v", err)
	}
}

func TestCollectSnapshotsEmptyDir(t *testing.T) {
	dir := t.TempDir()
	res := CollectSnapshots(dir, 5, nil)
	if len(res.Removed) != 0 || len(res.Kept) != 0 || len(res.Errors) != 0 {
		t.Fatalf("expected empty result for empty dir, got removed=%d kept=%d errors=%d",
			len(res.Removed), len(res.Kept), len(res.Errors))
	}
}

func TestCollectSnapshotsNonexistentDir(t *testing.T) {
	res := CollectSnapshots("/tmp/nonexistent-gc-test-dir-xyz", 5, nil)
	if len(res.Removed) != 0 || len(res.Kept) != 0 || len(res.Errors) != 0 {
		t.Fatalf("expected empty result for nonexistent dir, got removed=%d kept=%d errors=%d",
			len(res.Removed), len(res.Kept), len(res.Errors))
	}
}

func TestCollectSnapshotsIgnoresNonJSON(t *testing.T) {
	dir := t.TempDir()

	// Create a mix of .json and non-.json files.
	os.WriteFile(filepath.Join(dir, "snap-1.json"), []byte("{}"), 0o600)
	os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hello"), 0o600)
	os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("key: val"), 0o600)
	os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)

	res := CollectSnapshots(dir, 5, nil)
	// Only snap-1.json counted, within retain limit.
	if len(res.Kept) != 1 {
		t.Fatalf("expected 1 kept, got %d", len(res.Kept))
	}
	if len(res.Removed) != 0 {
		t.Fatalf("expected 0 removed, got %d", len(res.Removed))
	}
}

func TestCollectSnapshotsDefaultRetain(t *testing.T) {
	dir := t.TempDir()

	for i := 0; i < 7; i++ {
		p := filepath.Join(dir, fmt.Sprintf("s-%d.json", i))
		os.WriteFile(p, []byte("{}"), 0o600)
		ts := time.Now().Add(time.Duration(i-7) * time.Minute)
		os.Chtimes(p, ts, ts)
	}

	// retain=0 should default to 5.
	res := CollectSnapshots(dir, 0, nil)
	if len(res.Removed) != 2 {
		t.Fatalf("expected 2 removed with default retain=5, got %d", len(res.Removed))
	}
	if len(res.Kept) != 5 {
		t.Fatalf("expected 5 kept with default retain=5, got %d", len(res.Kept))
	}
}
