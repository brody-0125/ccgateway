package doctor

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	claudesettings "ccgateway/internal/claude"
	"ccgateway/internal/config"
	"ccgateway/internal/control"
	modelnorm "ccgateway/internal/model"
	policyguard "ccgateway/internal/policy"
	"ccgateway/internal/scope"
	"ccgateway/internal/state"
)

type CheckResult struct {
	Name   string
	OK     bool
	Detail string
}

type Report struct {
	Checks       []CheckResult
	RecentErrors []string
}

type ScopedInput struct {
	Scope  scope.Ref
	Paths  scope.Paths
	Config config.Config
	State  state.State
	Active control.ActivePointer
}

func Run(home string, cfg config.Config, proxyBinaryPath, logPath, authSource string) Report {
	checks := []CheckResult{
		fileCheck("config", filepath.Join(home, ".ccgateway", "config.yaml")),
		fileCheck("proxy binary", proxyBinaryPath),
		fileCheck("auth source", authSource),
		healthCheck(cfg.Port),
	}
	return Report{Checks: checks, RecentErrors: readRecentErrors(logPath, 5)}
}

func RunScoped(input ScopedInput) Report {
	checks := []CheckResult{
		fileCheck("config", input.Paths.ConfigPath),
		fileCheck("state", input.Paths.StatePath),
		settingsTargetCheck(input),
		authSourceRequirementCheck(input),
		modelPolicyCheck(input),
		policyGuardCheck(input),
	}
	proxyCheck := proxyRequirementCheck(input)
	checks = append(checks, proxyCheck)
	if input.Config.RuntimeMode == config.RuntimeModeGateway && proxyCheck.OK {
		checks = append(checks, healthCheck(input.Config.Port))
	}
	if input.Config.RuntimeMode == config.RuntimeModeNativeCleanup {
		checks = append(checks, nativeSettingsCheck(input))
	}
	if input.Config.RuntimeMode == config.RuntimeModeNativeDirect {
		checks = append(checks, nativeDirectSettingsCheck(input))
	}
	checks = append(checks, routeProofCheck(input))
	checks = append(checks, modelProofCheck(input))
	checks = append(checks, toolCallIntegrityCheck(input))
	checks = append(checks, activeGenerationCheck(input))
	return Report{Checks: checks, RecentErrors: readRecentErrors(input.Paths.AppLogPath, 5)}
}

func settingsTargetCheck(input ScopedInput) CheckResult {
	layer := input.Config.SettingsLayer
	path := strings.TrimSpace(input.Config.SettingsPath)
	switch layer {
	case config.SettingsLayerProject:
		expected := filepath.Clean(input.Paths.ClaudeProjectSettingsPath)
		if path == "" {
			return CheckResult{
				Name:   "settings target",
				OK:     false,
				Detail: fmt.Sprintf("settings_layer=project requires settings_path=%s; run: ccb setup --vendor %s --profile %s --settings-layer project", expected, input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		if filepath.Clean(path) != expected {
			return CheckResult{
				Name:   "settings target",
				OK:     false,
				Detail: fmt.Sprintf("settings_layer=project mismatch (configured=%s expected=%s for cwd=%s); run: ccb setup --vendor %s --profile %s --settings-layer project", path, expected, input.Paths.Cwd, input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		return CheckResult{Name: "settings target", OK: true, Detail: path}
	case config.SettingsLayerLocal:
		expected := filepath.Clean(input.Paths.ClaudeLocalSettingsPath)
		if path == "" {
			return CheckResult{
				Name:   "settings target",
				OK:     false,
				Detail: fmt.Sprintf("settings_layer=local requires settings_path=%s; run: ccb setup --vendor %s --profile %s --settings-layer local", expected, input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		if filepath.Clean(path) != expected {
			return CheckResult{
				Name:   "settings target",
				OK:     false,
				Detail: fmt.Sprintf("settings_layer=local mismatch (configured=%s expected=%s for cwd=%s); run: ccb setup --vendor %s --profile %s --settings-layer local", path, expected, input.Paths.Cwd, input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		return CheckResult{Name: "settings target", OK: true, Detail: path}
	case config.SettingsLayerUser:
		if path == "" {
			return CheckResult{
				Name:   "settings target",
				OK:     false,
				Detail: fmt.Sprintf("settings_layer=user requires settings_path; run: ccb setup --vendor %s --profile %s --settings-layer user --settings-path ~/.claude/settings.json", input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		return CheckResult{Name: "settings target", OK: true, Detail: path}
	default:
		return CheckResult{Name: "settings target", OK: false, Detail: fmt.Sprintf("unsupported settings_layer=%q", layer)}
	}
}

func modelPolicyCheck(input ScopedInput) CheckResult {
	model := strings.TrimSpace(input.Config.Model)
	switch input.Config.RuntimeMode {
	case config.RuntimeModeGateway:
		if !input.Config.ProxyEnabled {
			return CheckResult{Name: "model policy", OK: true, Detail: "not enforced (proxy disabled)"}
		}
		if model == "" {
			return CheckResult{
				Name:   "model policy",
				OK:     false,
				Detail: fmt.Sprintf("model is empty; run: ccb bootstrap --vendor %s --profile %s --model gpt-5.3-codex", input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		if _, err := modelnorm.NormalizeForVendor(input.Scope.VendorID, model); err != nil {
			return CheckResult{
				Name:   "model policy",
				OK:     false,
				Detail: fmt.Sprintf("invalid model for vendor=%s: %v; for Claude failover run: ccb failover --from %s:%s --to claude:default --model claude-opus-4-6", input.Scope.VendorID, err, input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		return CheckResult{Name: "model policy", OK: true, Detail: model}
	case config.RuntimeModeNativeDirect:
		if model == "" {
			return CheckResult{
				Name:   "model policy",
				OK:     false,
				Detail: fmt.Sprintf("model is empty for runtime_mode=%s; run: ccb bootstrap --vendor %s --profile %s --runtime-mode native-direct --model claude-opus-4-6", config.RuntimeModeNativeDirect, input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		return CheckResult{Name: "model policy", OK: true, Detail: model}
	case config.RuntimeModeNativeCleanup:
		return CheckResult{Name: "model policy", OK: true, Detail: "not enforced in runtime_mode=native-cleanup"}
	default:
		return CheckResult{Name: "model policy", OK: false, Detail: fmt.Sprintf("unsupported runtime_mode=%q", input.Config.RuntimeMode)}
	}
}

func policyGuardCheck(input ScopedInput) CheckResult {
	ev := policyguard.Evaluate(input.Paths, input.Config)
	if ev.Mode != policyguard.ModeStrict {
		return CheckResult{Name: "policy guard", OK: true, Detail: fmt.Sprintf("mode=%s", ev.Mode)}
	}
	if len(ev.Violations) == 0 {
		return CheckResult{Name: "policy guard", OK: true, Detail: "mode=strict"}
	}
	first := ev.Violations[0]
	if strings.Contains(first, "settings_layer=user") {
		return CheckResult{
			Name:   "policy guard",
			OK:     false,
			Detail: fmt.Sprintf("%s; run: ccb setup --vendor %s --profile %s --settings-layer project", first, input.Scope.VendorID, input.Scope.ProfileID),
		}
	}
	return CheckResult{
		Name:   "policy guard",
		OK:     false,
		Detail: fmt.Sprintf("%s; run: ccb bootstrap --vendor %s --profile %s", first, input.Scope.VendorID, input.Scope.ProfileID),
	}
}

func (r Report) HasFailures() bool {
	for _, c := range r.Checks {
		if !c.OK {
			return true
		}
	}
	return false
}

func (r Report) Render() string {
	var b strings.Builder
	for _, c := range r.Checks {
		status := "OK"
		if !c.OK {
			status = "FAIL"
		}
		_, _ = fmt.Fprintf(&b, "[%s] %s: %s\n", status, c.Name, c.Detail)
	}
	if hint, ok := topRecoveryHint(r.Checks); ok {
		_, _ = fmt.Fprintf(&b, "Top recovery: %s\n", hint)
	}
	if len(r.RecentErrors) > 0 {
		b.WriteString("Past errors (history only; current checks above determine pass/fail):\n")
		for _, line := range r.RecentErrors {
			_, _ = fmt.Fprintf(&b, "- %s\n", line)
		}
		b.WriteString("Tip: run `ccb doctor ... --clear-error-history` to reset error history.\n")
	}
	return strings.TrimSpace(b.String())
}

func topRecoveryHint(checks []CheckResult) (string, bool) {
	for _, c := range checks {
		if c.OK {
			continue
		}
		detail := strings.TrimSpace(c.Detail)
		if detail == "" {
			continue
		}
		lower := strings.ToLower(detail)
		if idx := strings.Index(lower, "run:"); idx >= 0 {
			cmd := strings.TrimSpace(detail[idx+len("run:"):])
			if cmd != "" {
				return cmd, true
			}
		}
	}
	return "", false
}

func ClearErrorHistory(logPath string) error {
	in, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	lines := strings.Split(string(in), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.Contains(line, "[ERROR]") {
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		kept = append(kept, line)
	}
	out := ""
	if len(kept) > 0 {
		out = strings.Join(kept, "\n") + "\n"
	}
	return writeAtomic(logPath, []byte(out), 0o644)
}

func fileCheck(name, path string) CheckResult {
	if _, err := os.Stat(path); err != nil {
		return CheckResult{Name: name, OK: false, Detail: fmt.Sprintf("missing (%s)", path)}
	}
	return CheckResult{Name: name, OK: true, Detail: path}
}

func proxyRequirementCheck(input ScopedInput) CheckResult {
	switch input.Config.RuntimeMode {
	case config.RuntimeModeGateway:
		if strings.TrimSpace(input.Config.GatewayBackend) == "" {
			return CheckResult{
				Name:   "gateway backend",
				OK:     false,
				Detail: fmt.Sprintf("gateway_backend is empty; run: ccb bootstrap --vendor %s --profile %s --gateway-backend cliproxyapi", input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		if !input.Config.ProxyEnabled {
			return CheckResult{
				Name:   "proxy requirement",
				OK:     false,
				Detail: "runtime_mode=gateway requires proxy_enabled=true",
			}
		}
		if _, err := os.Stat(input.Paths.ProxyBinary); err != nil {
			return CheckResult{
				Name:   "proxy binary",
				OK:     false,
				Detail: fmt.Sprintf("missing (%s); run: ccb proxy install --vendor %s --profile %s", input.Paths.ProxyBinary, input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		return CheckResult{Name: "proxy binary", OK: true, Detail: input.Paths.ProxyBinary}
	case config.RuntimeModeNativeCleanup:
		return CheckResult{Name: "proxy binary", OK: true, Detail: "not required in runtime_mode=native-cleanup"}
	case config.RuntimeModeNativeDirect:
		return CheckResult{Name: "proxy binary", OK: true, Detail: "not required in runtime_mode=native-direct"}
	default:
		return CheckResult{Name: "runtime mode", OK: false, Detail: fmt.Sprintf("unsupported runtime_mode=%q", input.Config.RuntimeMode)}
	}
}

func authSourceRequirementCheck(input ScopedInput) CheckResult {
	switch input.Config.RuntimeMode {
	case config.RuntimeModeGateway:
		if !input.Config.ProxyEnabled {
			return CheckResult{Name: "auth source", OK: true, Detail: "not required when proxy_enabled=false"}
		}
		return fileCheck("auth source", input.Config.AuthSource)
	case config.RuntimeModeNativeCleanup:
		return CheckResult{Name: "auth source", OK: true, Detail: "not required in runtime_mode=native-cleanup"}
	case config.RuntimeModeNativeDirect:
		return CheckResult{Name: "auth source", OK: true, Detail: "not required in runtime_mode=native-direct"}
	default:
		return CheckResult{Name: "auth source", OK: false, Detail: fmt.Sprintf("unsupported runtime_mode=%q", input.Config.RuntimeMode)}
	}
}

func nativeSettingsCheck(input ScopedInput) CheckResult {
	path := strings.TrimSpace(input.Config.SettingsPath)
	if path == "" {
		return CheckResult{Name: "native settings", OK: true, Detail: "settings_path is empty (skipped)"}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CheckResult{Name: "native settings", OK: true, Detail: fmt.Sprintf("settings not found yet (%s)", path)}
		}
		return CheckResult{Name: "native settings", OK: false, Detail: err.Error()}
	}
	if len(b) == 0 {
		return CheckResult{Name: "native settings", OK: true, Detail: path}
	}
	doc := map[string]any{}
	if err := json.Unmarshal(b, &doc); err != nil {
		return CheckResult{Name: "native settings", OK: false, Detail: fmt.Sprintf("invalid JSON (%s): %v", path, err)}
	}
	if _, ok := doc["model"]; ok {
		return CheckResult{
			Name:   "native settings",
			OK:     false,
			Detail: fmt.Sprintf("top-level model override exists; run: ccb claude apply --vendor %s --profile %s", input.Scope.VendorID, input.Scope.ProfileID),
		}
	}
	env, _ := doc["env"].(map[string]any)
	baseURL := asString(env["ANTHROPIC_BASE_URL"])
	if isLocalProxyBaseURL(baseURL) {
		return CheckResult{
			Name:   "native settings",
			OK:     false,
			Detail: fmt.Sprintf("ANTHROPIC_BASE_URL points to local proxy (%s); run: ccb claude apply --vendor %s --profile %s", baseURL, input.Scope.VendorID, input.Scope.ProfileID),
		}
	}
	token := asString(env["ANTHROPIC_AUTH_TOKEN"])
	if isManagedProxyToken(token) {
		return CheckResult{
			Name:   "native settings",
			OK:     false,
			Detail: fmt.Sprintf("ANTHROPIC_AUTH_TOKEN looks proxy-managed; run: ccb claude apply --vendor %s --profile %s", input.Scope.VendorID, input.Scope.ProfileID),
		}
	}
	for _, key := range managedModelEnvKeys() {
		if _, ok := env[key]; ok {
			return CheckResult{
				Name:   "native settings",
				OK:     false,
				Detail: fmt.Sprintf("%s should be removed in native cleanup mode; run: ccb claude apply --vendor %s --profile %s", key, input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
	}
	return CheckResult{Name: "native settings", OK: true, Detail: path}
}

func nativeDirectSettingsCheck(input ScopedInput) CheckResult {
	path := strings.TrimSpace(input.Config.SettingsPath)
	if path == "" {
		return CheckResult{Name: "native direct settings", OK: false, Detail: "settings_path is empty"}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CheckResult{Name: "native direct settings", OK: false, Detail: fmt.Sprintf("settings not found (%s); run: ccb claude apply --vendor %s --profile %s", path, input.Scope.VendorID, input.Scope.ProfileID)}
		}
		return CheckResult{Name: "native direct settings", OK: false, Detail: err.Error()}
	}
	doc := map[string]any{}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &doc); err != nil {
			return CheckResult{Name: "native direct settings", OK: false, Detail: fmt.Sprintf("invalid JSON (%s): %v", path, err)}
		}
	}
	if asString(doc["model"]) != input.Config.Model {
		return CheckResult{
			Name:   "native direct settings",
			OK:     false,
			Detail: fmt.Sprintf("settings model mismatch (want=%q got=%q); run: ccb claude apply --vendor %s --profile %s", input.Config.Model, asString(doc["model"]), input.Scope.VendorID, input.Scope.ProfileID),
		}
	}
	env, _ := doc["env"].(map[string]any)
	if isLocalProxyBaseURL(asString(env["ANTHROPIC_BASE_URL"])) {
		return CheckResult{
			Name:   "native direct settings",
			OK:     false,
			Detail: fmt.Sprintf("ANTHROPIC_BASE_URL still points to local proxy; run: ccb claude apply --vendor %s --profile %s", input.Scope.VendorID, input.Scope.ProfileID),
		}
	}
	if isManagedProxyToken(asString(env["ANTHROPIC_AUTH_TOKEN"])) {
		return CheckResult{
			Name:   "native direct settings",
			OK:     false,
			Detail: fmt.Sprintf("ANTHROPIC_AUTH_TOKEN still looks proxy-managed; run: ccb claude apply --vendor %s --profile %s", input.Scope.VendorID, input.Scope.ProfileID),
		}
	}
	if asString(env["ANTHROPIC_MODEL"]) != input.Config.Model {
		return CheckResult{
			Name:   "native direct settings",
			OK:     false,
			Detail: fmt.Sprintf("ANTHROPIC_MODEL mismatch (want=%q got=%q); run: ccb claude apply --vendor %s --profile %s", input.Config.Model, asString(env["ANTHROPIC_MODEL"]), input.Scope.VendorID, input.Scope.ProfileID),
		}
	}
	return CheckResult{Name: "native direct settings", OK: true, Detail: path}
}

func routeProofCheck(input ScopedInput) CheckResult {
	switch input.Config.RuntimeMode {
	case config.RuntimeModeGateway:
		path := strings.TrimSpace(input.Config.SettingsPath)
		if path == "" {
			return CheckResult{
				Name:   "route proof",
				OK:     false,
				Detail: fmt.Sprintf("settings_path is empty; run: ccb claude apply --vendor %s --profile %s", input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return CheckResult{
					Name:   "route proof",
					OK:     false,
					Detail: fmt.Sprintf("settings not found (%s); run: ccb claude apply --vendor %s --profile %s", path, input.Scope.VendorID, input.Scope.ProfileID),
				}
			}
			return CheckResult{Name: "route proof", OK: false, Detail: err.Error()}
		}
		doc := map[string]any{}
		if len(b) > 0 {
			if err := json.Unmarshal(b, &doc); err != nil {
				return CheckResult{Name: "route proof", OK: false, Detail: fmt.Sprintf("invalid settings JSON (%s): %v", path, err)}
			}
		}
		env, _ := doc["env"].(map[string]any)
		wantBase := fmt.Sprintf("http://127.0.0.1:%d", input.Config.Port)
		wantTokenPrefix := fmt.Sprintf("ccb::%s::%s::", input.Scope.VendorID, input.Scope.ProfileID)
		gotModel := asString(doc["model"])
		gotBase := asString(env["ANTHROPIC_BASE_URL"])
		gotToken := asString(env["ANTHROPIC_AUTH_TOKEN"])

		if gotModel != input.Config.Model {
			return CheckResult{
				Name:   "route proof",
				OK:     false,
				Detail: fmt.Sprintf("settings model mismatch (want=%q got=%q); run: ccb claude apply --vendor %s --profile %s", input.Config.Model, gotModel, input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		if gotBase != wantBase {
			return CheckResult{
				Name:   "route proof",
				OK:     false,
				Detail: fmt.Sprintf("ANTHROPIC_BASE_URL mismatch (want=%q got=%q); run: ccb claude apply --vendor %s --profile %s", wantBase, gotBase, input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		if !(strings.HasPrefix(gotToken, wantTokenPrefix) || gotToken == "proxy-local") {
			return CheckResult{
				Name:   "route proof",
				OK:     false,
				Detail: fmt.Sprintf("ANTHROPIC_AUTH_TOKEN is not ccgateway-managed; run: ccb claude apply --vendor %s --profile %s", input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		return CheckResult{
			Name: "route proof",
			OK:   true,
			Detail: fmt.Sprintf(
				"claude -> %s -> backend=%s -> model=%s",
				wantBase,
				input.Config.GatewayBackend,
				input.Config.Model,
			),
		}
	case config.RuntimeModeNativeCleanup:
		return CheckResult{
			Name:   "route proof",
			OK:     true,
			Detail: "runtime_mode=native-cleanup (proxy route disabled; cleanup-only path)",
		}
	case config.RuntimeModeNativeDirect:
		path := strings.TrimSpace(input.Config.SettingsPath)
		if path == "" {
			return CheckResult{
				Name:   "route proof",
				OK:     false,
				Detail: fmt.Sprintf("settings_path is empty; run: ccb claude apply --vendor %s --profile %s", input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		b, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return CheckResult{
					Name:   "route proof",
					OK:     false,
					Detail: fmt.Sprintf("settings not found (%s); run: ccb claude apply --vendor %s --profile %s", path, input.Scope.VendorID, input.Scope.ProfileID),
				}
			}
			return CheckResult{Name: "route proof", OK: false, Detail: err.Error()}
		}
		doc := map[string]any{}
		if len(b) > 0 {
			if err := json.Unmarshal(b, &doc); err != nil {
				return CheckResult{Name: "route proof", OK: false, Detail: fmt.Sprintf("invalid settings JSON (%s): %v", path, err)}
			}
		}
		env, _ := doc["env"].(map[string]any)
		gotModel := asString(doc["model"])
		if gotModel != input.Config.Model {
			return CheckResult{
				Name:   "route proof",
				OK:     false,
				Detail: fmt.Sprintf("settings model mismatch (want=%q got=%q); run: ccb claude apply --vendor %s --profile %s", input.Config.Model, gotModel, input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		if isLocalProxyBaseURL(asString(env["ANTHROPIC_BASE_URL"])) {
			return CheckResult{
				Name:   "route proof",
				OK:     false,
				Detail: fmt.Sprintf("ANTHROPIC_BASE_URL still points local proxy; run: ccb claude apply --vendor %s --profile %s", input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		if isManagedProxyToken(asString(env["ANTHROPIC_AUTH_TOKEN"])) {
			return CheckResult{
				Name:   "route proof",
				OK:     false,
				Detail: fmt.Sprintf("ANTHROPIC_AUTH_TOKEN still proxy-managed; run: ccb claude apply --vendor %s --profile %s", input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		return CheckResult{
			Name:   "route proof",
			OK:     true,
			Detail: fmt.Sprintf("claude -> native-direct -> model=%s", input.Config.Model),
		}
	default:
		return CheckResult{
			Name:   "route proof",
			OK:     false,
			Detail: fmt.Sprintf("unsupported runtime_mode=%q", input.Config.RuntimeMode),
		}
	}
}

func modelProofCheck(input ScopedInput) CheckResult {
	switch input.Config.RuntimeMode {
	case config.RuntimeModeGateway:
		if !input.Config.ProxyEnabled {
			return CheckResult{Name: "model proof", OK: true, Detail: "not enforced when proxy_enabled=false"}
		}
		b, err := os.ReadFile(input.Paths.ProxyConfig)
		if err != nil {
			if os.IsNotExist(err) {
				return CheckResult{
					Name:   "model proof",
					OK:     false,
					Detail: fmt.Sprintf("proxy config missing (%s); run: ccb service install --vendor %s --profile %s", input.Paths.ProxyConfig, input.Scope.VendorID, input.Scope.ProfileID),
				}
			}
			return CheckResult{Name: "model proof", OK: false, Detail: err.Error()}
		}
		if !strings.Contains(string(b), input.Config.Model) {
			return CheckResult{
				Name:   "model proof",
				OK:     false,
				Detail: fmt.Sprintf("proxy config model mismatch (want=%q); run: ccb service install --vendor %s --profile %s", input.Config.Model, input.Scope.VendorID, input.Scope.ProfileID),
			}
		}
		return CheckResult{Name: "model proof", OK: true, Detail: fmt.Sprintf("proxy config matches model=%s", input.Config.Model)}
	case config.RuntimeModeNativeDirect:
		if strings.TrimSpace(input.Config.Model) == "" {
			return CheckResult{Name: "model proof", OK: false, Detail: "model is empty in runtime_mode=native-direct"}
		}
		return CheckResult{Name: "model proof", OK: true, Detail: fmt.Sprintf("native-direct model=%s", input.Config.Model)}
	case config.RuntimeModeNativeCleanup:
		return CheckResult{Name: "model proof", OK: true, Detail: "not enforced in runtime_mode=native-cleanup"}
	default:
		return CheckResult{Name: "model proof", OK: false, Detail: fmt.Sprintf("unsupported runtime_mode=%q", input.Config.RuntimeMode)}
	}
}

type proxyTranscriptStats struct {
	Files            int
	EmptyInputHits   int
	MissingParamHits int
	AuthUnavailable  int
	StreamClosed     int
}

func toolCallIntegrityCheck(input ScopedInput) CheckResult {
	if input.Config.RuntimeMode != config.RuntimeModeGateway || !input.Config.ProxyEnabled {
		return CheckResult{
			Name:   "tool-call integrity",
			OK:     true,
			Detail: fmt.Sprintf("not enforced in runtime_mode=%s", input.Config.RuntimeMode),
		}
	}
	logDir := filepath.Join(input.Paths.ProxyDir, "logs")
	stats, err := scanProxyTranscriptIssues(logDir, 30*time.Minute, 20)
	if err != nil {
		if os.IsNotExist(err) {
			return CheckResult{Name: "tool-call integrity", OK: true, Detail: "no proxy transcript logs yet"}
		}
		return CheckResult{Name: "tool-call integrity", OK: false, Detail: err.Error()}
	}
	if stats.Files == 0 {
		return CheckResult{Name: "tool-call integrity", OK: true, Detail: "no recent proxy transcript errors"}
	}

	malformedToolCalls := stats.MissingParamHits >= 3 && stats.EmptyInputHits >= 1
	upstreamStability := stats.AuthUnavailable >= 1 || stats.StreamClosed >= 2
	if !(malformedToolCalls || upstreamStability) {
		return CheckResult{
			Name: "tool-call integrity",
			OK:   true,
			Detail: fmt.Sprintf(
				"recent transcript errors inspected (files=%d, empty_input=%d, missing_param=%d, auth_unavailable=%d, stream_closed=%d)",
				stats.Files,
				stats.EmptyInputHits,
				stats.MissingParamHits,
				stats.AuthUnavailable,
				stats.StreamClosed,
			),
		}
	}

	recovery := fmt.Sprintf(
		"run: ccb auth sync --vendor %s --profile %s; ccb service install --vendor %s --profile %s; ccb service start --vendor %s --profile %s",
		input.Scope.VendorID,
		input.Scope.ProfileID,
		input.Scope.VendorID,
		input.Scope.ProfileID,
		input.Scope.VendorID,
		input.Scope.ProfileID,
	)
	if strings.EqualFold(input.Scope.VendorID, "codex") && strings.EqualFold(input.Config.Model, modelnorm.CodexSparkModel) {
		recovery += fmt.Sprintf("; ccb model switch --vendor %s --profile %s --model %s", input.Scope.VendorID, input.Scope.ProfileID, modelnorm.CodexModel)
	}

	return CheckResult{
		Name: "tool-call integrity",
		OK:   false,
		Detail: fmt.Sprintf(
			"detected malformed/unstable proxy tool traffic in recent transcripts (files=%d, empty_input=%d, missing_param=%d, auth_unavailable=%d, stream_closed=%d); %s",
			stats.Files,
			stats.EmptyInputHits,
			stats.MissingParamHits,
			stats.AuthUnavailable,
			stats.StreamClosed,
			recovery,
		),
	}
}

func scanProxyTranscriptIssues(logDir string, window time.Duration, maxFiles int) (proxyTranscriptStats, error) {
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return proxyTranscriptStats{}, err
	}
	type transcriptFile struct {
		path    string
		modTime time.Time
	}
	candidates := make([]transcriptFile, 0, len(entries))
	cutoff := time.Now().Add(-window)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			continue
		}
		if !strings.HasPrefix(name, "error-v1-messages-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		info, infoErr := e.Info()
		if infoErr != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			continue
		}
		candidates = append(candidates, transcriptFile{
			path:    filepath.Join(logDir, name),
			modTime: info.ModTime(),
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	if maxFiles > 0 && len(candidates) > maxFiles {
		candidates = candidates[:maxFiles]
	}

	stats := proxyTranscriptStats{}
	for _, f := range candidates {
		b, readErr := os.ReadFile(f.path)
		if readErr != nil {
			continue
		}
		stats.Files++
		text := string(b)
		stats.EmptyInputHits += strings.Count(text, `"input":{}`)
		stats.EmptyInputHits += strings.Count(text, `"input": {}`)
		stats.MissingParamHits += strings.Count(text, "The required parameter `")
		stats.AuthUnavailable += strings.Count(text, "auth_unavailable: no auth available")
		stats.StreamClosed += strings.Count(text, "stream disconnected before completion")
	}
	return stats, nil
}

func healthCheck(port int) CheckResult {
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/models", port)
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return CheckResult{Name: "proxy health", OK: false, Detail: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return CheckResult{Name: "proxy health", OK: false, Detail: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return CheckResult{Name: "proxy health", OK: true, Detail: url}
}

func activeGenerationCheck(input ScopedInput) CheckResult {
	if input.Active.ActiveVendor == "" || input.Active.ActiveProfile == "" {
		return CheckResult{Name: "active generation", OK: true, Detail: "active scope not set"}
	}
	if input.Active.ActiveVendor != input.Scope.VendorID || input.Active.ActiveProfile != input.Scope.ProfileID {
		return CheckResult{Name: "active generation", OK: true, Detail: "scope is not active"}
	}
	if !input.State.Claude.Applied {
		if input.Config.RuntimeMode == config.RuntimeModeNativeCleanup {
			return CheckResult{Name: "active generation", OK: true, Detail: "active scope has no applied claude settings"}
		}
		return CheckResult{Name: "active generation", OK: false, Detail: "active scope has no applied claude settings"}
	}
	if input.State.Claude.AppliedGeneration == "" {
		return CheckResult{Name: "active generation", OK: false, Detail: "active scope has empty applied_generation"}
	}
	if input.Active.ActiveGeneration != input.State.Claude.AppliedGeneration {
		return CheckResult{
			Name:   "active generation",
			OK:     false,
			Detail: fmt.Sprintf("mismatch active=%s state=%s", input.Active.ActiveGeneration, input.State.Claude.AppliedGeneration),
		}
	}
	return CheckResult{Name: "active generation", OK: true, Detail: input.Active.ActiveGeneration}
}

func readRecentErrors(logPath string, limit int) []string {
	f, err := os.Open(logPath)
	if err != nil {
		return nil
	}
	defer f.Close()
	lines := []string{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if strings.Contains(line, "[ERROR]") {
			lines = append(lines, line)
		}
	}
	if len(lines) <= limit {
		return lines
	}
	return lines[len(lines)-limit:]
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

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func isLocalProxyBaseURL(v string) bool {
	s := strings.ToLower(strings.TrimSpace(v))
	return strings.HasPrefix(s, "http://127.0.0.1:") || strings.HasPrefix(s, "http://localhost:")
}

func isManagedProxyToken(v string) bool {
	s := strings.TrimSpace(v)
	return s == "proxy-local" || strings.HasPrefix(s, "ccb::")
}

func managedModelEnvKeys() []string {
	return claudesettings.ManagedModelEnvKeys()
}
