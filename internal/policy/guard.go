package policy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"ccgateway/internal/config"
	"ccgateway/internal/scope"
)

const (
	ModeStrict = "strict"
	ModeCompat = "compat"
)

type Evaluation struct {
	Mode       string
	Violations []string
}

func NormalizeMode(v string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", ModeStrict:
		return ModeStrict, true
	case ModeCompat:
		return ModeCompat, true
	default:
		return "", false
	}
}

func DefaultModeForVendor(vendorID string) string {
	if strings.EqualFold(strings.TrimSpace(vendorID), "codex") {
		return ModeStrict
	}
	return ModeCompat
}

func Evaluate(paths scope.Paths, cfg config.Config) Evaluation {
	mode := strings.TrimSpace(cfg.PolicyMode)
	if normalized, ok := NormalizeMode(mode); ok {
		mode = normalized
	} else {
		mode = DefaultModeForVendor(cfg.VendorID)
	}
	if strings.EqualFold(strings.TrimSpace(cfg.VendorID), "codex") {
		// Codex policy is always strict. Explicit compatibility bypass is not supported.
		mode = ModeStrict
	}
	out := Evaluation{Mode: mode}
	if mode != ModeStrict {
		return out
	}
	if !strings.EqualFold(strings.TrimSpace(cfg.VendorID), "codex") {
		return out
	}

	if cfg.RuntimeMode != config.RuntimeModeGateway && cfg.RuntimeMode != config.RuntimeModeNativeCleanup {
		out.Violations = append(out.Violations, fmt.Sprintf("runtime_mode=%q is not allowed for vendor=codex", cfg.RuntimeMode))
	}

	if cfg.SettingsLayer == config.SettingsLayerUser {
		out.Violations = append(out.Violations, "settings_layer=user is blocked in strict mode; use settings_layer=project|local")
	}

	settingsPath := strings.TrimSpace(cfg.SettingsPath)
	if settingsPath != "" && cfg.SettingsLayer != config.SettingsLayerUser {
		if !pathWithin(settingsPath, paths.Cwd) {
			out.Violations = append(out.Violations, fmt.Sprintf("settings_path=%s escapes current project cwd=%s", settingsPath, paths.Cwd))
		}
	}

	authTarget := strings.TrimSpace(cfg.AuthTarget)
	if authTarget != "" {
		allowedGlobalAuthDir := filepath.Join(paths.BaseDir, "auths")
		if !pathWithin(authTarget, paths.ScopeDir) && !pathWithin(authTarget, allowedGlobalAuthDir) {
			out.Violations = append(out.Violations, fmt.Sprintf("auth_target=%s escapes scope/global auth dir", authTarget))
		}
	}

	if cfg.RuntimeMode == config.RuntimeModeGateway && cfg.ProxyEnabled {
		checkAuthSourceOwnership(&out, strings.TrimSpace(cfg.AuthSource))
	}
	return out
}

func checkAuthSourceOwnership(out *Evaluation, authSource string) {
	if out == nil || authSource == "" {
		return
	}
	info, err := os.Stat(authSource)
	if err != nil {
		if os.IsNotExist(err) {
			// File existence is handled by runtime commands/doctor; policy guard only validates boundary/ownership when present.
			return
		}
		out.Violations = append(out.Violations, fmt.Sprintf("failed to stat auth_source=%s: %v", authSource, err))
		return
	}
	if info.Mode().Perm() != 0o600 {
		out.Violations = append(out.Violations, fmt.Sprintf("auth_source=%s must have mode 0600 (got %o)", authSource, info.Mode().Perm()))
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if int(stat.Uid) != os.Getuid() {
			out.Violations = append(out.Violations, fmt.Sprintf("auth_source=%s must be owned by current uid=%d (got uid=%d)", authSource, os.Getuid(), stat.Uid))
		}
	}
}

func pathWithin(path, root string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	root = filepath.Clean(strings.TrimSpace(root))
	if path == "" || root == "" {
		return false
	}
	if path == root {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && rel != ".."
}
