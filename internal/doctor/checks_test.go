package doctor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ccgateway/internal/config"
	"ccgateway/internal/control"
	"ccgateway/internal/scope"
	"ccgateway/internal/state"
)

func TestRunScopedNativeDoesNotRequireProxyBinary(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeNativeCleanup
	cfg.ProxyEnabled = false
	cfg.AuthSource = paths.AuthSource
	cfg.SettingsPath = paths.ClaudeUserSettingsPath
	if err := os.MkdirAll(filepath.Dir(cfg.SettingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir failed: %v", err)
	}
	if err := os.WriteFile(cfg.SettingsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write settings failed: %v", err)
	}

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})
	if report.HasFailures() {
		t.Fatalf("unexpected failures:\n%s", report.Render())
	}

	found := false
	for _, c := range report.Checks {
		if c.Name == "proxy binary" {
			found = true
			if !c.OK || !strings.Contains(c.Detail, "not required") {
				t.Fatalf("unexpected proxy check: %+v", c)
			}
		}
	}
	if !found {
		t.Fatal("expected proxy binary check entry")
	}
}

func TestReportRenderIncludesTopRecoveryHint(t *testing.T) {
	report := Report{
		Checks: []CheckResult{
			{Name: "a", OK: true, Detail: "ok"},
			{Name: "b", OK: false, Detail: "missing proxy; run: ccb service reconcile --vendor codex --profile default"},
		},
	}
	rendered := report.Render()
	if !strings.Contains(rendered, "Top recovery: ccb service reconcile --vendor codex --profile default") {
		t.Fatalf("expected top recovery hint in render, got:\n%s", rendered)
	}
}

func TestRunScopedGatewayMissingProxyShowsInstallHint(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeGateway
	cfg.ProxyEnabled = true
	cfg.AuthSource = paths.AuthSource
	cfg.Port = 1

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})

	var proxyCheck *CheckResult
	for idx := range report.Checks {
		if report.Checks[idx].Name == "proxy binary" {
			proxyCheck = &report.Checks[idx]
			break
		}
	}
	if proxyCheck == nil {
		t.Fatalf("proxy check missing:\n%s", report.Render())
	}
	if proxyCheck.OK {
		t.Fatalf("expected proxy check failure, got: %+v", proxyCheck)
	}
	if !strings.Contains(proxyCheck.Detail, "ccb proxy install --vendor codex --profile default") {
		t.Fatalf("expected install hint, got: %s", proxyCheck.Detail)
	}
}

func TestRunScopedDetectsProjectSettingsPathMismatch(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "workspace-a")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, cwd, ref)
	prepareScopedFiles(t, paths)

	cfg := config.DefaultForScope(home, cwd, ref.VendorID, ref.ProfileID)
	cfg.SettingsLayer = config.SettingsLayerProject
	cfg.SettingsPath = filepath.Join(home, "workspace-b", ".claude", "settings.json")
	cfg.RuntimeMode = config.RuntimeModeNativeCleanup
	cfg.ProxyEnabled = false
	cfg.AuthSource = paths.AuthSource

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})

	var targetCheck *CheckResult
	for idx := range report.Checks {
		if report.Checks[idx].Name == "settings target" {
			targetCheck = &report.Checks[idx]
			break
		}
	}
	if targetCheck == nil {
		t.Fatalf("settings target check missing:\n%s", report.Render())
	}
	if targetCheck.OK {
		t.Fatalf("expected settings target mismatch failure, got: %+v", targetCheck)
	}
	if !strings.Contains(targetCheck.Detail, "--settings-layer project") {
		t.Fatalf("expected remediation hint, got: %s", targetCheck.Detail)
	}
}

func TestRunScopedGatewayMissingBackendShowsBootstrapHint(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeGateway
	cfg.ProxyEnabled = true
	cfg.GatewayBackend = ""
	cfg.AuthSource = paths.AuthSource
	cfg.Port = 1

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})

	var backendCheck *CheckResult
	for idx := range report.Checks {
		if report.Checks[idx].Name == "gateway backend" {
			backendCheck = &report.Checks[idx]
			break
		}
	}
	if backendCheck == nil {
		t.Fatalf("gateway backend check missing:\n%s", report.Render())
	}
	if backendCheck.OK {
		t.Fatalf("expected backend check failure, got: %+v", backendCheck)
	}
	if !strings.Contains(backendCheck.Detail, "--gateway-backend cliproxyapi") {
		t.Fatalf("expected backend bootstrap hint, got: %s", backendCheck.Detail)
	}
}

func TestRunScopedGatewayRejectsClaudeSelectorModel(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeGateway
	cfg.ProxyEnabled = true
	cfg.AuthSource = paths.AuthSource
	cfg.Model = "claude-opus-4-6"
	cfg.Port = 1

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})

	var modelCheck *CheckResult
	for idx := range report.Checks {
		if report.Checks[idx].Name == "model policy" {
			modelCheck = &report.Checks[idx]
			break
		}
	}
	if modelCheck == nil {
		t.Fatalf("model policy check missing:\n%s", report.Render())
	}
	if modelCheck.OK {
		t.Fatalf("expected model policy failure, got: %+v", modelCheck)
	}
	if !strings.Contains(modelCheck.Detail, "ccb failover") {
		t.Fatalf("expected failover hint, got: %s", modelCheck.Detail)
	}
}

func TestRunScopedNativeDetectsLocalProxySettingLeak(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeNativeCleanup
	cfg.ProxyEnabled = false
	cfg.AuthSource = paths.AuthSource
	cfg.SettingsPath = paths.ClaudeUserSettingsPath
	if err := os.MkdirAll(filepath.Dir(cfg.SettingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir failed: %v", err)
	}
	leaked := []byte("{\"env\":{\"ANTHROPIC_BASE_URL\":\"http://127.0.0.1:8317\",\"ANTHROPIC_AUTH_TOKEN\":\"ccb::codex::default::gen-1\"}}\n")
	if err := os.WriteFile(cfg.SettingsPath, leaked, 0o600); err != nil {
		t.Fatalf("write leaked settings failed: %v", err)
	}

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})

	var nativeCheck *CheckResult
	for idx := range report.Checks {
		if report.Checks[idx].Name == "native settings" {
			nativeCheck = &report.Checks[idx]
			break
		}
	}
	if nativeCheck == nil {
		t.Fatalf("native settings check missing:\n%s", report.Render())
	}
	if nativeCheck.OK {
		t.Fatalf("expected native settings failure, got: %+v", nativeCheck)
	}
	if !strings.Contains(nativeCheck.Detail, "ccb claude apply --vendor codex --profile default") {
		t.Fatalf("expected remediation hint, got: %s", nativeCheck.Detail)
	}
}

func TestRunScopedNativeDetectsModelOverrideLeak(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeNativeCleanup
	cfg.ProxyEnabled = false
	cfg.AuthSource = paths.AuthSource
	cfg.SettingsPath = paths.ClaudeUserSettingsPath
	if err := os.MkdirAll(filepath.Dir(cfg.SettingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir failed: %v", err)
	}
	leaked := []byte("{\"model\":\"gpt-5.3-codex\",\"env\":{\"ANTHROPIC_MODEL\":\"gpt-5.3-codex\"}}\n")
	if err := os.WriteFile(cfg.SettingsPath, leaked, 0o600); err != nil {
		t.Fatalf("write leaked settings failed: %v", err)
	}

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})

	var nativeCheck *CheckResult
	for idx := range report.Checks {
		if report.Checks[idx].Name == "native settings" {
			nativeCheck = &report.Checks[idx]
			break
		}
	}
	if nativeCheck == nil {
		t.Fatalf("native settings check missing:\n%s", report.Render())
	}
	if nativeCheck.OK {
		t.Fatalf("expected native settings failure, got: %+v", nativeCheck)
	}
	if !strings.Contains(nativeCheck.Detail, "ccb claude apply --vendor codex --profile default") {
		t.Fatalf("expected remediation hint, got: %s", nativeCheck.Detail)
	}
}

func TestRunScopedNativeMissingAuthSourceIsNotFailure(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)
	if err := os.Remove(paths.AuthSource); err != nil {
		t.Fatalf("remove auth source failed: %v", err)
	}

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeNativeCleanup
	cfg.ProxyEnabled = false
	cfg.AuthSource = paths.AuthSource
	cfg.SettingsPath = paths.ClaudeUserSettingsPath
	if err := os.MkdirAll(filepath.Dir(cfg.SettingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir failed: %v", err)
	}
	if err := os.WriteFile(cfg.SettingsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write settings failed: %v", err)
	}

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})

	var authCheck *CheckResult
	for idx := range report.Checks {
		if report.Checks[idx].Name == "auth source" {
			authCheck = &report.Checks[idx]
			break
		}
	}
	if authCheck == nil {
		t.Fatalf("auth source check missing:\n%s", report.Render())
	}
	if !authCheck.OK {
		t.Fatalf("native mode should not require auth source: %+v", authCheck)
	}
}

func TestRunScopedActiveCleanScopeDoesNotFailGeneration(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeNativeCleanup
	cfg.ProxyEnabled = false
	cfg.AuthSource = paths.AuthSource
	cfg.SettingsPath = paths.ClaudeUserSettingsPath
	if err := os.MkdirAll(filepath.Dir(cfg.SettingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir failed: %v", err)
	}
	if err := os.WriteFile(cfg.SettingsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write settings failed: %v", err)
	}

	st := state.Default()
	st.Scope.VendorID = ref.VendorID
	st.Scope.ProfileID = ref.ProfileID
	st.RuntimeMode = string(config.RuntimeModeNativeCleanup)
	st.Claude.Applied = false
	st.Claude.AppliedGeneration = ""

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  st,
		Active: control.ActivePointer{
			ActiveVendor:     ref.VendorID,
			ActiveProfile:    ref.ProfileID,
			ActiveGeneration: "gen-stale",
		},
	})

	var genCheck *CheckResult
	for idx := range report.Checks {
		if report.Checks[idx].Name == "active generation" {
			genCheck = &report.Checks[idx]
			break
		}
	}
	if genCheck == nil {
		t.Fatalf("active generation check missing:\n%s", report.Render())
	}
	if !genCheck.OK {
		t.Fatalf("clean active scope should not fail generation check: %+v", genCheck)
	}
}

func TestRunScopedActiveGatewayWithoutAppliedFailsGeneration(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeGateway
	cfg.ProxyEnabled = true
	cfg.AuthSource = paths.AuthSource
	cfg.Port = 1

	st := state.Default()
	st.Scope.VendorID = ref.VendorID
	st.Scope.ProfileID = ref.ProfileID
	st.RuntimeMode = string(config.RuntimeModeGateway)
	st.Claude.Applied = false
	st.Claude.AppliedGeneration = ""

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  st,
		Active: control.ActivePointer{
			ActiveVendor:     ref.VendorID,
			ActiveProfile:    ref.ProfileID,
			ActiveGeneration: "gen-stale",
		},
	})

	var genCheck *CheckResult
	for idx := range report.Checks {
		if report.Checks[idx].Name == "active generation" {
			genCheck = &report.Checks[idx]
			break
		}
	}
	if genCheck == nil {
		t.Fatalf("active generation check missing:\n%s", report.Render())
	}
	if genCheck.OK {
		t.Fatalf("gateway active scope without applied settings must fail generation check: %+v", genCheck)
	}
}

func TestRunScopedGatewayRouteProofPassesWhenSettingsApplied(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)
	if err := os.MkdirAll(paths.ProxyDir, 0o755); err != nil {
		t.Fatalf("mkdir proxy dir failed: %v", err)
	}
	if err := os.WriteFile(paths.ProxyBinary, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write proxy binary failed: %v", err)
	}

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeGateway
	cfg.ProxyEnabled = true
	cfg.GatewayBackend = "cliproxyapi"
	cfg.Port = 8317
	cfg.Model = "gpt-5.3-codex"
	cfg.AuthSource = paths.AuthSource
	cfg.SettingsPath = paths.ClaudeUserSettingsPath

	if err := os.MkdirAll(filepath.Dir(cfg.SettingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir failed: %v", err)
	}
	body := []byte("{\"model\":\"gpt-5.3-codex\",\"env\":{\"ANTHROPIC_BASE_URL\":\"http://127.0.0.1:8317\",\"ANTHROPIC_AUTH_TOKEN\":\"ccb::codex::default::gen-1\"}}\n")
	if err := os.WriteFile(cfg.SettingsPath, body, 0o600); err != nil {
		t.Fatalf("write settings failed: %v", err)
	}

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})

	var routeCheck *CheckResult
	for idx := range report.Checks {
		if report.Checks[idx].Name == "route proof" {
			routeCheck = &report.Checks[idx]
			break
		}
	}
	if routeCheck == nil {
		t.Fatalf("route proof check missing:\n%s", report.Render())
	}
	if !routeCheck.OK {
		t.Fatalf("expected route proof success, got: %+v", routeCheck)
	}
	if !strings.Contains(routeCheck.Detail, "backend=cliproxyapi") || !strings.Contains(routeCheck.Detail, "model=gpt-5.3-codex") {
		t.Fatalf("unexpected route proof detail: %s", routeCheck.Detail)
	}
}

func TestRunScopedGatewayRouteProofFailsWhenSettingsNotApplied(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)
	if err := os.MkdirAll(paths.ProxyDir, 0o755); err != nil {
		t.Fatalf("mkdir proxy dir failed: %v", err)
	}
	if err := os.WriteFile(paths.ProxyBinary, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write proxy binary failed: %v", err)
	}

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeGateway
	cfg.ProxyEnabled = true
	cfg.GatewayBackend = "cliproxyapi"
	cfg.Port = 8317
	cfg.Model = "gpt-5.3-codex"
	cfg.AuthSource = paths.AuthSource
	cfg.SettingsPath = paths.ClaudeUserSettingsPath
	if err := os.MkdirAll(filepath.Dir(cfg.SettingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir failed: %v", err)
	}
	if err := os.WriteFile(cfg.SettingsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write settings failed: %v", err)
	}

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})

	var routeCheck *CheckResult
	for idx := range report.Checks {
		if report.Checks[idx].Name == "route proof" {
			routeCheck = &report.Checks[idx]
			break
		}
	}
	if routeCheck == nil {
		t.Fatalf("route proof check missing:\n%s", report.Render())
	}
	if routeCheck.OK {
		t.Fatalf("expected route proof failure when settings not applied, got: %+v", routeCheck)
	}
	if !strings.Contains(routeCheck.Detail, "ccb claude apply --vendor codex --profile default") {
		t.Fatalf("expected remediation hint in route proof detail, got: %s", routeCheck.Detail)
	}
}

func TestRunScopedNativeRouteProofExplainsProxyDisabled(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeNativeCleanup
	cfg.ProxyEnabled = false
	cfg.AuthSource = paths.AuthSource
	cfg.SettingsPath = paths.ClaudeUserSettingsPath

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})

	var routeCheck *CheckResult
	for idx := range report.Checks {
		if report.Checks[idx].Name == "route proof" {
			routeCheck = &report.Checks[idx]
			break
		}
	}
	if routeCheck == nil {
		t.Fatalf("route proof check missing:\n%s", report.Render())
	}
	if !routeCheck.OK {
		t.Fatalf("native route proof should be informational success: %+v", routeCheck)
	}
	if !strings.Contains(routeCheck.Detail, "proxy route disabled") {
		t.Fatalf("unexpected native route proof detail: %s", routeCheck.Detail)
	}
}

func TestRunScopedNativeDirectRouteProofPasses(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("claude", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeNativeDirect
	cfg.ProxyEnabled = false
	cfg.Model = "claude-opus-4-6"
	cfg.SettingsPath = paths.ClaudeUserSettingsPath
	if err := os.MkdirAll(filepath.Dir(cfg.SettingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir failed: %v", err)
	}
	body := []byte("{\"model\":\"claude-opus-4-6\",\"env\":{\"ANTHROPIC_MODEL\":\"claude-opus-4-6\"}}\n")
	if err := os.WriteFile(cfg.SettingsPath, body, 0o600); err != nil {
		t.Fatalf("write settings failed: %v", err)
	}

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})

	var routeCheck *CheckResult
	var directCheck *CheckResult
	for idx := range report.Checks {
		if report.Checks[idx].Name == "route proof" {
			routeCheck = &report.Checks[idx]
		}
		if report.Checks[idx].Name == "native direct settings" {
			directCheck = &report.Checks[idx]
		}
	}
	if routeCheck == nil {
		t.Fatalf("route proof check missing:\n%s", report.Render())
	}
	if !routeCheck.OK {
		t.Fatalf("expected native-direct route proof success, got: %+v", routeCheck)
	}
	if !strings.Contains(routeCheck.Detail, "native-direct") {
		t.Fatalf("unexpected route proof detail: %s", routeCheck.Detail)
	}
	if directCheck == nil || !directCheck.OK {
		t.Fatalf("expected native direct settings check success, got: %+v\nreport:\n%s", directCheck, report.Render())
	}
}

func TestRunScopedNativeDirectRouteProofFailsOnLocalProxyLeak(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("claude", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeNativeDirect
	cfg.ProxyEnabled = false
	cfg.Model = "claude-opus-4-6"
	cfg.SettingsPath = paths.ClaudeUserSettingsPath
	if err := os.MkdirAll(filepath.Dir(cfg.SettingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir failed: %v", err)
	}
	body := []byte("{\"model\":\"claude-opus-4-6\",\"env\":{\"ANTHROPIC_MODEL\":\"claude-opus-4-6\",\"ANTHROPIC_BASE_URL\":\"http://127.0.0.1:8317\"}}\n")
	if err := os.WriteFile(cfg.SettingsPath, body, 0o600); err != nil {
		t.Fatalf("write settings failed: %v", err)
	}

	report := RunScoped(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})

	var routeCheck *CheckResult
	for idx := range report.Checks {
		if report.Checks[idx].Name == "route proof" {
			routeCheck = &report.Checks[idx]
			break
		}
	}
	if routeCheck == nil {
		t.Fatalf("route proof check missing:\n%s", report.Render())
	}
	if routeCheck.OK {
		t.Fatalf("expected native-direct route proof failure, got: %+v", routeCheck)
	}
	if !strings.Contains(routeCheck.Detail, "local proxy") {
		t.Fatalf("expected local proxy hint, got: %s", routeCheck.Detail)
	}
}

func TestClearErrorHistoryRemovesOnlyErrorLines(t *testing.T) {
	home := t.TempDir()
	logPath := filepath.Join(home, "app.log")
	content := strings.Join([]string{
		"2026-02-14T00:00:00Z [INFO] scope initialized",
		"2026-02-14T00:00:01Z [ERROR] code=ERR_TEST | failed",
		"2026-02-14T00:00:02Z [INFO] still healthy",
		"",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write log failed: %v", err)
	}

	if err := ClearErrorHistory(logPath); err != nil {
		t.Fatalf("ClearErrorHistory failed: %v", err)
	}
	b, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log failed: %v", err)
	}
	text := string(b)
	if strings.Contains(text, "[ERROR]") {
		t.Fatalf("expected error lines removed, got: %s", text)
	}
	if !strings.Contains(text, "[INFO] scope initialized") || !strings.Contains(text, "[INFO] still healthy") {
		t.Fatalf("expected info lines preserved, got: %s", text)
	}
}

func TestReportRenderLabelsErrorHistoryAsPast(t *testing.T) {
	report := Report{
		Checks: []CheckResult{
			{Name: "config", OK: true, Detail: "/tmp/config.yaml"},
		},
		RecentErrors: []string{"2026-02-14T00:00:01Z [ERROR] code=ERR_TEST | failed"},
	}
	out := report.Render()
	if !strings.Contains(out, "Past errors (history only; current checks above determine pass/fail):") {
		t.Fatalf("expected past-error label in render output, got:\n%s", out)
	}
	if !strings.Contains(out, "--clear-error-history") {
		t.Fatalf("expected clear hint in render output, got:\n%s", out)
	}
}

func TestToolCallIntegrityCheckNoRecentProxyTranscriptLogs(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeGateway
	cfg.ProxyEnabled = true
	cfg.Model = "gpt-5.3-codex"

	check := toolCallIntegrityCheck(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})
	if !check.OK {
		t.Fatalf("expected no-log integrity check success, got: %+v", check)
	}
	if !strings.Contains(check.Detail, "no proxy transcript logs") {
		t.Fatalf("unexpected integrity detail: %s", check.Detail)
	}
}

func TestToolCallIntegrityCheckDetectsMalformedToolTraffic(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	prepareScopedFiles(t, paths)
	if err := os.MkdirAll(filepath.Join(paths.ProxyDir, "logs"), 0o755); err != nil {
		t.Fatalf("mkdir proxy logs failed: %v", err)
	}

	logBody := strings.Join([]string{
		"Status: 500",
		`{"type":"tool_use","name":"WebSearch","input":{}}`,
		"The required parameter `query` is missing",
		"The required parameter `pattern` is missing",
		"The required parameter `command` is missing",
		"The required parameter `query` is missing",
		"The required parameter `query` is missing",
	}, "\n")
	logPath := filepath.Join(paths.ProxyDir, "logs", "error-v1-messages-test.log")
	if err := os.WriteFile(logPath, []byte(logBody), 0o644); err != nil {
		t.Fatalf("write proxy transcript log failed: %v", err)
	}

	cfg := config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID)
	cfg.RuntimeMode = config.RuntimeModeGateway
	cfg.ProxyEnabled = true
	cfg.Model = "gpt-5.3-codex-spark"

	check := toolCallIntegrityCheck(ScopedInput{
		Scope:  ref,
		Paths:  paths,
		Config: cfg,
		State:  state.Default(),
		Active: control.ActivePointer{},
	})
	if check.OK {
		t.Fatalf("expected malformed-tool integrity check failure, got: %+v", check)
	}
	if !strings.Contains(check.Detail, "malformed/unstable proxy tool traffic") {
		t.Fatalf("expected malformed traffic hint, got: %s", check.Detail)
	}
	if !strings.Contains(check.Detail, "ccb model switch --vendor codex --profile default --model gpt-5.3-codex") {
		t.Fatalf("expected codex fallback remediation hint, got: %s", check.Detail)
	}
}

func prepareScopedFiles(t *testing.T, paths scope.Paths) {
	t.Helper()
	for _, p := range []string{
		filepath.Dir(paths.ConfigPath),
		filepath.Dir(paths.StatePath),
		filepath.Dir(paths.AuthSource),
	} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatalf("mkdir failed for %s: %v", p, err)
		}
	}
	if err := os.WriteFile(paths.ConfigPath, []byte("schema_version: 2\n"), 0o644); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	if err := os.WriteFile(paths.StatePath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write state failed: %v", err)
	}
	if err := os.WriteFile(paths.AuthSource, []byte("{\"tokens\":{\"access_token\":\"x\"}}\n"), 0o600); err != nil {
		t.Fatalf("write auth source failed: %v", err)
	}
}
