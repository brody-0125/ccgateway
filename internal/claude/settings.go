package claude

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	cberr "ccgateway/internal/errors"
	"ccgateway/internal/state"
)

type ApplyResult struct {
	SnapshotPath   string
	SnapshotSHA256 string
}

type ApplyOptions struct {
	SettingsPath string
	SnapshotDir  string
	Port         int
	Model        string
	AuthToken    string
	Mode         string
}

const (
	ApplyModeGateway       = "gateway"
	ApplyModeNativeCleanup = "native-cleanup"
	ApplyModeNativeDirect  = "native-direct"
	// ApplyModeNative is a legacy alias preserved for compatibility.
	ApplyModeNative = ApplyModeNativeCleanup
)

func Apply(settingsPath, snapshotDir string, port int, model string) (ApplyResult, error) {
	return ApplyWithOptions(ApplyOptions{
		SettingsPath: settingsPath,
		SnapshotDir:  snapshotDir,
		Port:         port,
		Model:        model,
		AuthToken:    "proxy-local",
		Mode:         ApplyModeGateway,
	})
}

func ApplyWithOptions(opts ApplyOptions) (ApplyResult, error) {
	settingsPath := opts.SettingsPath
	snapshotDir := opts.SnapshotDir
	port := opts.Port
	model := opts.Model
	authToken := opts.AuthToken
	mode := normalizeApplyMode(opts.Mode)
	if authToken == "" {
		authToken = "proxy-local"
	}
	if mode != ApplyModeGateway && mode != ApplyModeNativeCleanup && mode != ApplyModeNativeDirect {
		return ApplyResult{}, cberr.New(cberr.ErrClaudeApplyFailed, "invalid apply mode")
	}

	original, err := readOrInitSettings(settingsPath)
	if err != nil {
		return ApplyResult{}, cberr.Wrap(cberr.ErrClaudeApplyFailed, "failed to read settings", err)
	}
	originalHash := state.HashBytes(original)

	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		return ApplyResult{}, cberr.Wrap(cberr.ErrClaudeApplyFailed, "failed to create snapshot directory", err)
	}
	snapshotPath := filepath.Join(snapshotDir, fmt.Sprintf("claude-settings-%d.json", time.Now().UTC().UnixNano()))
	if err := writeAtomic(snapshotPath, original, 0o600); err != nil {
		return ApplyResult{}, cberr.Wrap(cberr.ErrClaudeApplyFailed, "failed to write snapshot", err)
	}
	snapshotHash, err := state.HashFile(snapshotPath)
	if err != nil {
		return ApplyResult{}, cberr.Wrap(cberr.ErrClaudeApplyFailed, "failed to hash snapshot", err)
	}
	if snapshotHash != originalHash {
		return ApplyResult{}, cberr.New(cberr.ErrClaudeApplyFailed, "snapshot hash mismatch")
	}

	m := map[string]any{}
	if err := json.Unmarshal(original, &m); err != nil {
		return ApplyResult{}, cberr.Wrap(cberr.ErrClaudeApplyFailed, "failed to parse settings JSON", err)
	}

	env, ok := m["env"].(map[string]any)
	if !ok {
		env = map[string]any{}
	}

	switch mode {
	case ApplyModeGateway:
		m["model"] = model
		setModelEnv(env, model)
		env["ANTHROPIC_BASE_URL"] = fmt.Sprintf("http://127.0.0.1:%d", port)
		env["ANTHROPIC_AUTH_TOKEN"] = authToken
	case ApplyModeNativeCleanup:
		// Cleanup-only mode: remove ccgateway-managed routing/model overrides.
		delete(m, "model")
		clearManagedModelEnv(env)
		if isLocalProxyBaseURL(asString(env["ANTHROPIC_BASE_URL"])) {
			delete(env, "ANTHROPIC_BASE_URL")
		}
		if isManagedProxyToken(asString(env["ANTHROPIC_AUTH_TOKEN"])) {
			delete(env, "ANTHROPIC_AUTH_TOKEN")
		}
	case ApplyModeNativeDirect:
		// Direct native mode: configure model, but ensure local proxy routing is not kept.
		m["model"] = model
		setModelEnv(env, model)
		if isLocalProxyBaseURL(asString(env["ANTHROPIC_BASE_URL"])) {
			delete(env, "ANTHROPIC_BASE_URL")
		}
		if isManagedProxyToken(asString(env["ANTHROPIC_AUTH_TOKEN"])) {
			delete(env, "ANTHROPIC_AUTH_TOKEN")
		}
	}
	m["env"] = env

	updated, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return ApplyResult{}, cberr.Wrap(cberr.ErrClaudeApplyFailed, "failed to encode updated settings", err)
	}
	updated = append(updated, '\n')
	if err := writeAtomic(settingsPath, updated, 0o600); err != nil {
		return ApplyResult{}, cberr.Wrap(cberr.ErrClaudeApplyFailed, "failed to write updated settings", err)
	}

	if err := verifyApplied(settingsPath, port, model, mode); err != nil {
		return ApplyResult{}, cberr.Wrap(cberr.ErrClaudeApplyFailed, "post-apply verification failed", err)
	}

	return ApplyResult{SnapshotPath: snapshotPath, SnapshotSHA256: snapshotHash}, nil
}

// ManagedEnvKeys returns the complete set of env keys that ccgateway manages
// inside Claude settings. This includes model routing keys and proxy connection keys.
func ManagedEnvKeys() []string {
	return []string{
		"ANTHROPIC_BASE_URL",
		"ANTHROPIC_AUTH_TOKEN",
		"ANTHROPIC_MODEL",
		"ANTHROPIC_SMALL_FAST_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL",
		"ANTHROPIC_DEFAULT_OPUS_MODEL",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	}
}

// SmartRevert restores only ccgateway-managed keys to their pre-Apply values
// while preserving any user modifications to non-managed keys.
// Falls back to full Revert if the current settings file cannot be read or parsed.
func SmartRevert(settingsPath, snapshotPath, snapshotSHA string) error {
	if snapshotPath == "" {
		return cberr.New(cberr.ErrClaudeRevertFailed, "snapshot path is empty")
	}
	snapBytes, err := os.ReadFile(snapshotPath)
	if err != nil {
		return cberr.Wrap(cberr.ErrClaudeRevertFailed, "failed to read snapshot", err)
	}
	snapHash := state.HashBytes(snapBytes)
	if snapshotSHA != "" && snapHash != snapshotSHA {
		return cberr.New(cberr.ErrClaudeRevertFailed, fmt.Sprintf("snapshot hash mismatch expected=%s actual=%s", snapshotSHA, snapHash))
	}

	// Read current settings; fall back to full Revert only when the file is
	// missing or deleted. For transient errors (permission denied, etc.) return
	// the error to avoid silently discarding user changes.
	curBytes, err := os.ReadFile(settingsPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Revert(settingsPath, snapshotPath, snapshotSHA)
		}
		return cberr.Wrap(cberr.ErrClaudeRevertFailed, "failed to read current settings", err)
	}

	var snapDoc map[string]any
	if err := json.Unmarshal(snapBytes, &snapDoc); err != nil {
		return Revert(settingsPath, snapshotPath, snapshotSHA)
	}
	var curDoc map[string]any
	if err := json.Unmarshal(curBytes, &curDoc); err != nil {
		return Revert(settingsPath, snapshotPath, snapshotSHA)
	}

	// Restore top-level "model" from snapshot.
	if origVal, existed := snapDoc["model"]; existed {
		curDoc["model"] = origVal
	} else {
		delete(curDoc, "model")
	}

	// Restore managed env keys from snapshot.
	snapEnv, _ := snapDoc["env"].(map[string]any)
	_, snapHadEnv := snapDoc["env"]
	curEnv, curHasEnv := curDoc["env"].(map[string]any)
	if !curHasEnv {
		curEnv = map[string]any{}
	}

	for _, key := range ManagedEnvKeys() {
		if snapEnv != nil {
			if origVal, existed := snapEnv[key]; existed {
				curEnv[key] = origVal
				continue
			}
		}
		delete(curEnv, key)
	}

	if len(curEnv) > 0 {
		curDoc["env"] = curEnv
	} else if snapHadEnv {
		curDoc["env"] = curEnv
	} else {
		delete(curDoc, "env")
	}

	updated, err := json.MarshalIndent(curDoc, "", "  ")
	if err != nil {
		return cberr.Wrap(cberr.ErrClaudeRevertFailed, "failed to encode merged settings", err)
	}
	updated = append(updated, '\n')
	if err := writeAtomic(settingsPath, updated, 0o600); err != nil {
		return cberr.Wrap(cberr.ErrClaudeRevertFailed, "failed to write merged settings", err)
	}
	return nil
}

func Revert(settingsPath, snapshotPath, snapshotSHA string) error {
	if snapshotPath == "" {
		return cberr.New(cberr.ErrClaudeRevertFailed, "snapshot path is empty")
	}
	b, err := os.ReadFile(snapshotPath)
	if err != nil {
		return cberr.Wrap(cberr.ErrClaudeRevertFailed, "failed to read snapshot", err)
	}
	actual := state.HashBytes(b)
	if snapshotSHA != "" && actual != snapshotSHA {
		return cberr.New(cberr.ErrClaudeRevertFailed, fmt.Sprintf("snapshot hash mismatch expected=%s actual=%s", snapshotSHA, actual))
	}
	if err := writeAtomic(settingsPath, b, 0o600); err != nil {
		return cberr.Wrap(cberr.ErrClaudeRevertFailed, "failed to restore settings", err)
	}
	restoredHash, err := state.HashFile(settingsPath)
	if err != nil {
		return cberr.Wrap(cberr.ErrClaudeRevertFailed, "failed to hash restored settings", err)
	}
	if restoredHash != actual {
		return cberr.New(cberr.ErrClaudeRevertFailed, "restored settings hash mismatch")
	}
	return nil
}

func readOrInitSettings(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		if len(b) == 0 {
			b = []byte("{}\n")
		}
		return b, nil
	}
	if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	b = []byte("{}\n")
	if err := writeAtomic(path, b, 0o600); err != nil {
		return nil, err
	}
	return b, nil
}

func verifyApplied(settingsPath string, port int, model, mode string) error {
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		return err
	}
	m := map[string]any{}
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	env, ok := m["env"].(map[string]any)
	if !ok {
		return fmt.Errorf("env is missing")
	}
	switch mode {
	case ApplyModeGateway:
		if asString(m["model"]) != model {
			return fmt.Errorf("model mismatch")
		}
		if asString(env["ANTHROPIC_MODEL"]) != model {
			return fmt.Errorf("ANTHROPIC_MODEL mismatch")
		}
		wantBaseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
		if asString(env["ANTHROPIC_BASE_URL"]) != wantBaseURL {
			return fmt.Errorf("ANTHROPIC_BASE_URL mismatch")
		}
	case ApplyModeNativeCleanup:
		if _, exists := m["model"]; exists {
			return fmt.Errorf("model must be removed in native-cleanup mode")
		}
		for _, key := range managedModelEnvKeys() {
			if _, exists := env[key]; exists {
				return fmt.Errorf("%s must be removed in native-cleanup mode", key)
			}
		}
		if isLocalProxyBaseURL(asString(env["ANTHROPIC_BASE_URL"])) {
			return fmt.Errorf("ANTHROPIC_BASE_URL must not target local proxy in native-cleanup mode")
		}
		if isManagedProxyToken(asString(env["ANTHROPIC_AUTH_TOKEN"])) {
			return fmt.Errorf("ANTHROPIC_AUTH_TOKEN must not be proxy-managed in native-cleanup mode")
		}
	case ApplyModeNativeDirect:
		if asString(m["model"]) != model {
			return fmt.Errorf("model mismatch")
		}
		if asString(env["ANTHROPIC_MODEL"]) != model {
			return fmt.Errorf("ANTHROPIC_MODEL mismatch")
		}
		if isLocalProxyBaseURL(asString(env["ANTHROPIC_BASE_URL"])) {
			return fmt.Errorf("ANTHROPIC_BASE_URL must not target local proxy in native-direct mode")
		}
		if isManagedProxyToken(asString(env["ANTHROPIC_AUTH_TOKEN"])) {
			return fmt.Errorf("ANTHROPIC_AUTH_TOKEN must not be proxy-managed in native-direct mode")
		}
	default:
		return fmt.Errorf("unsupported apply mode")
	}
	return nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func normalizeApplyMode(v string) string {
	mode := strings.ToLower(strings.TrimSpace(v))
	if mode == "" {
		return ApplyModeGateway
	}
	if mode == "native" {
		return ApplyModeNativeCleanup
	}
	return mode
}

func setModelEnv(env map[string]any, model string) {
	env["ANTHROPIC_MODEL"] = model
	env["ANTHROPIC_SMALL_FAST_MODEL"] = model
	env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = model
	env["ANTHROPIC_DEFAULT_OPUS_MODEL"] = model
	env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] = model
}

func clearManagedModelEnv(env map[string]any) {
	for _, key := range managedModelEnvKeys() {
		delete(env, key)
	}
}

// ManagedModelEnvKeys returns the model-routing subset of ManagedEnvKeys
// (excludes proxy connection keys ANTHROPIC_BASE_URL and ANTHROPIC_AUTH_TOKEN).
func ManagedModelEnvKeys() []string {
	out := make([]string, 0, len(ManagedEnvKeys()))
	for _, k := range ManagedEnvKeys() {
		if k == "ANTHROPIC_BASE_URL" || k == "ANTHROPIC_AUTH_TOKEN" {
			continue
		}
		out = append(out, k)
	}
	return out
}

func managedModelEnvKeys() []string {
	return ManagedModelEnvKeys()
}

func isLocalProxyBaseURL(v string) bool {
	s := strings.ToLower(strings.TrimSpace(v))
	return strings.HasPrefix(s, "http://127.0.0.1:") || strings.HasPrefix(s, "http://localhost:")
}

func isManagedProxyToken(v string) bool {
	s := strings.TrimSpace(v)
	return s == "proxy-local" || strings.HasPrefix(s, "ccg::")
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", path, os.Getpid(), time.Now().UTC().UnixNano())
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
