package cli

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ccgateway/internal/backend"
	builtinbackend "ccgateway/internal/backend/builtin"
	"ccgateway/internal/backends"
	claudepkg "ccgateway/internal/claude"
	"ccgateway/internal/config"
	"ccgateway/internal/control"
	cberr "ccgateway/internal/errors"
	"ccgateway/internal/provider"
	providerclaude "ccgateway/internal/provider/claudevendor"
	providercodex "ccgateway/internal/provider/codex"
	"ccgateway/internal/providers"
	"ccgateway/internal/scope"
	"ccgateway/internal/state"
)

func TestPromptWithDefault(t *testing.T) {
	in := strings.NewReader("\ncustom\n")
	reader := bufio.NewReader(in)
	var out bytes.Buffer

	got, err := promptWithDefault(reader, &out, "vendor", "codex")
	if err != nil {
		t.Fatalf("promptWithDefault first call failed: %v", err)
	}
	if got != "codex" {
		t.Fatalf("expected default value, got %q", got)
	}

	got, err = promptWithDefault(reader, &out, "profile", "default")
	if err != nil {
		t.Fatalf("promptWithDefault second call failed: %v", err)
	}
	if got != "custom" {
		t.Fatalf("expected custom value, got %q", got)
	}
}

func TestLaunchAgentPlistPathsPreferRuntimeLabels(t *testing.T) {
	tmpHome := t.TempDir()
	tmpCwd := filepath.Join(tmpHome, "repo")
	if err := os.MkdirAll(tmpCwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd failed: %v", err)
	}
	paths := scope.BuildPaths(tmpHome, tmpCwd, scope.MustRef("codex", "default"))
	paths.LaunchAgentDir = filepath.Join(tmpHome, "Library", "LaunchAgents")
	paths.ProxyPlistPath = filepath.Join(paths.LaunchAgentDir, "com.legacy.proxy.plist")
	paths.SyncPlistPath = filepath.Join(paths.LaunchAgentDir, "com.legacy.sync.plist")

	proxyPath, syncPath := launchAgentPlistPaths(paths, "com.real.proxy", "com.real.sync")
	if want := filepath.Join(paths.LaunchAgentDir, "com.real.proxy.plist"); proxyPath != want {
		t.Fatalf("unexpected proxy plist path: got=%s want=%s", proxyPath, want)
	}
	if want := filepath.Join(paths.LaunchAgentDir, "com.real.sync.plist"); syncPath != want {
		t.Fatalf("unexpected sync plist path: got=%s want=%s", syncPath, want)
	}
}

func TestLaunchAgentPlistPathsFallbackWhenLabelsEmpty(t *testing.T) {
	tmpHome := t.TempDir()
	tmpCwd := filepath.Join(tmpHome, "repo")
	if err := os.MkdirAll(tmpCwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd failed: %v", err)
	}
	paths := scope.BuildPaths(tmpHome, tmpCwd, scope.MustRef("codex", "default"))

	proxyPath, syncPath := launchAgentPlistPaths(paths, "", "")
	if proxyPath != paths.ProxyPlistPath {
		t.Fatalf("expected proxy fallback path=%s, got=%s", paths.ProxyPlistPath, proxyPath)
	}
	if syncPath != paths.SyncPlistPath {
		t.Fatalf("expected sync fallback path=%s, got=%s", paths.SyncPlistPath, syncPath)
	}
}

func TestServiceMutatingSubcommandsRejectActiveFlag(t *testing.T) {
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	for _, sub := range []string{"install", "start", "reconcile", "stop"} {
		err := app.cmdService([]string{sub, "--active"})
		if err == nil {
			t.Fatalf("%s should reject --active", sub)
		}
		if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
			t.Fatalf("%s returned unexpected error code: %s (%v)", sub, code, err)
		}
	}
}

func TestServiceStatusAllowsActiveFlagPath(t *testing.T) {
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	err = app.cmdService([]string{"status", "--active"})
	if err == nil {
		// If local active scope is configured on the test machine, status may succeed.
		return
	}

	if code := cberr.Code(err); code == cberr.ErrInvalidArgs {
		t.Fatalf("status --active should not fail as invalid args: %v", err)
	}
}

func TestServiceRejectsUnexpectedPositionalArgs(t *testing.T) {
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	err = app.cmdService([]string{"status", "junk"})
	if err == nil {
		t.Fatal("status should reject unexpected positional args")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestProxyInstallRejectsUnexpectedPositionalArgs(t *testing.T) {
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	err = app.cmdProxy([]string{"install", "--vendor", "codex", "--profile", "default", "junk"})
	if err == nil {
		t.Fatal("proxy install should reject unexpected positional args")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestProxyInstallRejectsNativeMode(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "default", "--runtime-mode", "gateway"}); err != nil {
		t.Fatalf("bootstrap gateway failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "default", "--runtime-mode", "native"}); err != nil {
		t.Fatalf("bootstrap native failed: %v", err)
	}

	err = app.cmdProxy([]string{"install", "--vendor", "codex", "--profile", "default"})
	if err == nil {
		t.Fatal("expected native mode proxy install rejection")
	}
	if code := cberr.Code(err); code != cberr.ErrCapabilityMissing {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "runtime_mode=gateway") {
		t.Fatalf("expected gateway-mode hint, got: %v", err)
	}
}

func TestSetupRejectsUnexpectedPositionalArgs(t *testing.T) {
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	err = app.cmdSetup([]string{"--vendor", "codex", "--profile", "default", "junk"})
	if err == nil {
		t.Fatal("setup should reject unexpected positional args")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestSetupRejectsInvalidRuntimeMode(t *testing.T) {
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	err = app.cmdSetup([]string{"--vendor", "codex", "--profile", "default", "--runtime-mode", "broken"})
	if err == nil {
		t.Fatal("setup should reject invalid runtime mode")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestSetupRejectsInvalidSettingsLayer(t *testing.T) {
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	err = app.cmdSetup([]string{"--vendor", "codex", "--profile", "default", "--settings-layer", "broken"})
	if err == nil {
		t.Fatal("setup should reject invalid settings layer")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestSetupNormalizesCodexSparkModelAlias(t *testing.T) {
	tmpHome := t.TempDir()
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nexit 0\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)

	reg := provider.NewRegistry()
	if err := reg.Register(provider.Bundle{
		VendorID:     "codex",
		RuntimeModes: []string{string(config.RuntimeModeGateway), string(config.RuntimeModeNativeCleanup)},
		Capabilities: map[provider.Capability]bool{
			provider.CapabilityAuth: true,
		},
		Auth: noopAuthStrategy{},
	}); err != nil {
		t.Fatalf("register provider failed: %v", err)
	}
	backendReg := backend.NewRegistry()
	if err := backendReg.Register(backend.Bundle{
		ID: "stub-backend",
		Capabilities: map[backend.Capability]bool{
			backend.CapabilityArtifact: true,
			backend.CapabilityProxy:    true,
			backend.CapabilityHealth:   true,
		},
		Artifact: fakeBackendArtifactInstaller{},
		Proxy:    noopBackendProxyRenderer{},
		Health:   noopBackendHealthChecker{},
	}); err != nil {
		t.Fatalf("register backend failed: %v", err)
	}
	app := &application{home: tmpHome, cwd: tmpHome, username: "tester", registry: reg, backendRegistry: backendReg}
	ref := scope.MustRef("codex", "default")

	if err := app.cmdSetup([]string{
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--gateway-backend", "stub-backend",
		"--model", "codex-spark",
		"--skip-claude-apply",
		"--skip-doctor",
	}); err != nil {
		t.Fatalf("setup failed: %v", err)
	}
	rt, err := app.loadRuntime(ref, false)
	if err != nil {
		t.Fatalf("load runtime failed: %v", err)
	}
	if rt.Config.Model != "gpt-5.3-codex-spark" {
		t.Fatalf("expected normalized spark model, got %q", rt.Config.Model)
	}
}

func TestSetupAppliesProjectSettingsLayer(t *testing.T) {
	tmpHome := t.TempDir()
	tmpCwd := filepath.Join(tmpHome, "workspace")
	if err := os.MkdirAll(tmpCwd, 0o755); err != nil {
		t.Fatalf("mkdir workspace failed: %v", err)
	}
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nexit 0\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpCwd)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)

	reg := provider.NewRegistry()
	if err := reg.Register(provider.Bundle{
		VendorID:     "codex",
		RuntimeModes: []string{string(config.RuntimeModeGateway), string(config.RuntimeModeNativeCleanup)},
		Capabilities: map[provider.Capability]bool{
			provider.CapabilityAuth: true,
		},
		Auth: noopAuthStrategy{},
	}); err != nil {
		t.Fatalf("register provider failed: %v", err)
	}
	backendReg := backend.NewRegistry()
	if err := backendReg.Register(backend.Bundle{
		ID: "stub-backend",
		Capabilities: map[backend.Capability]bool{
			backend.CapabilityArtifact: true,
			backend.CapabilityProxy:    true,
			backend.CapabilityHealth:   true,
		},
		Artifact: fakeBackendArtifactInstaller{},
		Proxy:    noopBackendProxyRenderer{},
		Health:   noopBackendHealthChecker{},
	}); err != nil {
		t.Fatalf("register backend failed: %v", err)
	}
	app := &application{home: tmpHome, cwd: tmpCwd, username: "tester", registry: reg, backendRegistry: backendReg}
	ref := scope.MustRef("codex", "default")

	if err := app.cmdSetup([]string{
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--gateway-backend", "stub-backend",
		"--settings-layer", "project",
		"--skip-claude-apply",
		"--skip-doctor",
	}); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	rt, err := app.loadRuntime(ref, false)
	if err != nil {
		t.Fatalf("load runtime failed: %v", err)
	}
	if rt.Config.SettingsLayer != config.SettingsLayerProject {
		t.Fatalf("expected settings_layer=project, got %q", rt.Config.SettingsLayer)
	}
	wantPath := filepath.Join(tmpCwd, ".claude", "settings.json")
	if rt.Config.SettingsPath != wantPath {
		t.Fatalf("expected settings_path=%q, got %q", wantPath, rt.Config.SettingsPath)
	}
}

func TestBootstrapRejectsClaudeSelectorModelForCodex(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	err = app.cmdBootstrap([]string{
		"--vendor", "codex",
		"--profile", "default",
		"--model", "claude-opus-4-6",
	})
	if err == nil {
		t.Fatal("expected bootstrap failure for claude selector model in codex scope")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidConfig {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "cannot be used with vendor=codex") {
		t.Fatalf("expected codex model-policy hint, got: %v", err)
	}
}

func TestBootstrapPolicyStrictRejectsUserSettingsLayer(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	err = app.cmdBootstrap([]string{
		"--vendor", "codex",
		"--profile", "default",
		"--settings-layer", "user",
		"--settings-path", filepath.Join(tmpHome, ".claude", "settings.json"),
		"--policy-mode", "strict",
	})
	if err == nil {
		t.Fatal("expected strict policy violation for settings-layer=user")
	}
	if code := cberr.Code(err); code != cberr.ErrPolicyViolation {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestBootstrapPolicyCompatRejectedForCodex(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	err = app.cmdBootstrap([]string{
		"--vendor", "codex",
		"--profile", "default",
		"--settings-layer", "user",
		"--settings-path", filepath.Join(tmpHome, ".claude", "settings.json"),
		"--policy-mode", "compat",
	})
	if err == nil {
		t.Fatal("expected rejection for codex policy-mode compat")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestBootstrapSettingsLayerChangeResolvesDefaultPath(t *testing.T) {
	tmpHome := t.TempDir()
	tmpCwd := filepath.Join(tmpHome, "repo")
	if err := os.MkdirAll(tmpCwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)

	app := &application{
		home:            tmpHome,
		cwd:             tmpCwd,
		username:        "tester",
		registry:        providers.DefaultRegistry(),
		backendRegistry: backends.DefaultRegistry(),
	}
	ref := scope.MustRef("claude", "default")

	if err := app.cmdBootstrap([]string{
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--settings-layer", "user",
		"--settings-path", filepath.Join(tmpHome, ".claude", "settings.json"),
	}); err != nil {
		t.Fatalf("bootstrap with user settings failed: %v", err)
	}

	if err := app.cmdBootstrap([]string{
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--settings-layer", "project",
	}); err != nil {
		t.Fatalf("bootstrap with project settings failed: %v", err)
	}

	rt, err := app.loadRuntime(ref, false)
	if err != nil {
		t.Fatalf("load runtime failed: %v", err)
	}
	if rt.Config.SettingsLayer != config.SettingsLayerProject {
		t.Fatalf("expected settings layer=project, got %q", rt.Config.SettingsLayer)
	}
	wantPath := filepath.Join(tmpCwd, ".claude", "settings.json")
	if rt.Config.SettingsPath != wantPath {
		t.Fatalf("expected settings path %q, got %q", wantPath, rt.Config.SettingsPath)
	}
}

func TestModelSwitchRejectsInactiveScope(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "default"}); err != nil {
		t.Fatalf("bootstrap default failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "alt"}); err != nil {
		t.Fatalf("bootstrap alt failed: %v", err)
	}

	err = app.cmdModel([]string{"switch", "--vendor", "codex", "--profile", "alt", "--model", "codex-spark"})
	if err == nil {
		t.Fatal("expected inactive scope switch rejection")
	}
	if code := cberr.Code(err); code != cberr.ErrSwitchValidation {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "active-scope only") {
		t.Fatalf("expected active-scope-only hint, got: %v", err)
	}
}

func TestModelSwitchRejectsNativeScope(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "default", "--runtime-mode", "gateway"}); err != nil {
		t.Fatalf("bootstrap gateway failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "default", "--runtime-mode", "native"}); err != nil {
		t.Fatalf("bootstrap native failed: %v", err)
	}

	err = app.cmdModel([]string{"switch", "--vendor", "codex", "--profile", "default", "--model", "codex-spark"})
	if err == nil {
		t.Fatal("expected native scope switch rejection")
	}
	if code := cberr.Code(err); code != cberr.ErrCapabilityMissing {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "runtime_mode=gateway") {
		t.Fatalf("expected gateway-mode hint, got: %v", err)
	}
}

func TestModelSwitchRejectsClaudeSelectorModelForCodex(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "default"}); err != nil {
		t.Fatalf("bootstrap default failed: %v", err)
	}

	err = app.cmdModel([]string{"switch", "--vendor", "codex", "--profile", "default", "--model", "claude-opus-4-6"})
	if err == nil {
		t.Fatal("expected model switch rejection for claude selector model in codex scope")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "cannot be used with vendor=codex") {
		t.Fatalf("expected codex model-policy hint, got: %v", err)
	}
}

func TestModelCommandAcceptsDashHelp(t *testing.T) {
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	if err := app.cmdModel([]string{"-help"}); err != nil {
		t.Fatalf("model -help should succeed: %v", err)
	}
}

func TestModelSwitchFailsFastWhenProxyBinaryMissing(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}

	err = app.cmdModel([]string{
		"switch",
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--model", "gpt-5.3-codex",
	})
	if err == nil {
		t.Fatal("expected model switch failure when proxy binary is missing")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidConfig {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "proxy binary missing") {
		t.Fatalf("expected missing binary hint, got: %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "rollback") {
		t.Fatalf("should fail before transaction/rollback, got: %v", err)
	}
}

func TestGatewayRejectsUnknownSubcommand(t *testing.T) {
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	err = app.cmdGateway([]string{"unknown"})
	if err == nil {
		t.Fatal("expected unknown gateway subcommand rejection")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestGatewayServeRequiresConfig(t *testing.T) {
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	err = app.cmdGateway([]string{"serve"})
	if err == nil {
		t.Fatal("expected missing --config rejection")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestGatewayServeRejectsUnexpectedPositionalArgs(t *testing.T) {
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	err = app.cmdGateway([]string{"serve", "--config", "/tmp/fake.json", "junk"})
	if err == nil {
		t.Fatal("expected unexpected positional args rejection")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestGatewayServeRejectsNonBuiltinBackendConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "gateway.json")
	if err := builtinbackend.WriteConfig(cfgPath, builtinbackend.ServeConfig{
		Listen:    "127.0.0.1:18317",
		Model:     "m-test",
		BackendID: "cliproxyapi",
	}); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	err = app.cmdGateway([]string{"serve", "--config", cfgPath})
	if err == nil {
		t.Fatal("expected backend mismatch rejection")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidConfig {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "backend_id") {
		t.Fatalf("expected backend mismatch detail, got: %v", err)
	}
}

func TestUseRejectsUnexpectedPositionalArgs(t *testing.T) {
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	err = app.cmdUse([]string{"--vendor", "codex", "--profile", "default", "junk"})
	if err == nil {
		t.Fatal("use should reject unexpected positional args")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestSetupNativeCleanupFlow(t *testing.T) {
	tmpHome := t.TempDir()
	logFile := filepath.Join(tmpHome, "launchctl.log")
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >> \"$CCB_TEST_LAUNCHCTL_LOG\"\nprintf 'Could not find service\\n' >&2\nexit 1\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)
	t.Setenv("CCB_TEST_LAUNCHCTL_LOG", logFile)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	ref := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--runtime-mode", "gateway"}); err != nil {
		t.Fatalf("bootstrap gateway failed: %v", err)
	}

	err = app.cmdSetup([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--runtime-mode", "native", "--skip-doctor"})
	if err != nil {
		t.Fatalf("setup native failed: %v", err)
	}

	rt, err := app.loadRuntime(ref, false)
	if err != nil {
		t.Fatalf("load runtime failed: %v", err)
	}
	if string(rt.Config.RuntimeMode) != string(config.RuntimeModeNativeCleanup) {
		t.Fatalf("expected runtime mode %s, got %q", config.RuntimeModeNativeCleanup, rt.Config.RuntimeMode)
	}
	if rt.State.Service.Running {
		t.Fatalf("expected service running=false after native cleanup: %+v", rt.State.Service)
	}
	if !rt.State.Claude.Applied {
		t.Fatalf("expected claude applied=true after setup native: %+v", rt.State.Claude)
	}
	if rt.State.Claude.AppliedGeneration == "" {
		t.Fatalf("expected non-empty applied generation: %+v", rt.State.Claude)
	}
	logs, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read launchctl log failed: %v", err)
	}
	if !strings.Contains(string(logs), "bootout") {
		t.Fatalf("expected service cleanup bootout call, got: %s", string(logs))
	}
}

func TestSetupRecoversServiceStartByReinstallingAgents(t *testing.T) {
	tmpHome := t.TempDir()
	stub := filepath.Join(tmpHome, "launchctl")
	marker := filepath.Join(tmpHome, "kickstart-sync.failed.once")
	stubScript := "#!/usr/bin/env bash\nset -euo pipefail\ncmd=\"${1:-}\"\nmarker=\"${CCB_TEST_RECOVER_MARKER:-/tmp/ccb-recover-marker}\"\nif [[ \"$cmd\" == \"kickstart\" ]]; then\n  target=\"${3:-}\"\n  if [[ \"$target\" == *\".sync\" ]] && [[ ! -f \"$marker\" ]]; then\n    touch \"$marker\"\n    printf 'Could not find service\\n' >&2\n    exit 113\n  fi\nfi\nif [[ \"$cmd\" == \"print\" ]]; then\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)
	t.Setenv("CCB_TEST_RECOVER_MARKER", marker)

	reg := provider.NewRegistry()
	if err := reg.Register(provider.Bundle{
		VendorID:     "stub",
		RuntimeModes: []string{"gateway"},
		Capabilities: map[provider.Capability]bool{
			provider.CapabilityAuth: true,
		},
		Auth: noopAuthStrategy{},
	}); err != nil {
		t.Fatalf("register provider failed: %v", err)
	}
	backendReg := backend.NewRegistry()
	if err := backendReg.Register(backend.Bundle{
		ID: "stub-backend",
		Capabilities: map[backend.Capability]bool{
			backend.CapabilityArtifact: true,
			backend.CapabilityProxy:    true,
			backend.CapabilityHealth:   true,
		},
		Artifact: fakeBackendArtifactInstaller{},
		Proxy:    noopBackendProxyRenderer{},
		Health:   noopBackendHealthChecker{},
	}); err != nil {
		t.Fatalf("register backend failed: %v", err)
	}
	app := &application{home: tmpHome, cwd: tmpHome, username: "tester", registry: reg, backendRegistry: backendReg}
	ref := scope.MustRef("stub", "default")

	if err := app.cmdSetup([]string{
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--runtime-mode", "gateway",
		"--gateway-backend", "stub-backend",
		"--skip-claude-apply",
		"--skip-doctor",
	}); err != nil {
		t.Fatalf("setup with auto-recovery should succeed: %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected marker file to confirm initial start failure path: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)
	st, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	if !st.Service.Running {
		t.Fatalf("expected service running=true after setup recovery: %+v", st.Service)
	}
}

func TestServiceReconcileInstallsAndStartsService(t *testing.T) {
	tmpHome := t.TempDir()
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nset -euo pipefail\nif [[ \"${1:-}\" == \"print\" ]]; then\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)

	reg := provider.NewRegistry()
	if err := reg.Register(provider.Bundle{
		VendorID:     "stub",
		RuntimeModes: []string{"gateway"},
		Capabilities: map[provider.Capability]bool{
			provider.CapabilityAuth: true,
		},
		Auth: noopAuthStrategy{},
	}); err != nil {
		t.Fatalf("register provider failed: %v", err)
	}
	backendReg := backend.NewRegistry()
	if err := backendReg.Register(backend.Bundle{
		ID: "stub-backend",
		Capabilities: map[backend.Capability]bool{
			backend.CapabilityArtifact: true,
			backend.CapabilityProxy:    true,
			backend.CapabilityHealth:   true,
		},
		Artifact: fakeBackendArtifactInstaller{},
		Proxy:    noopBackendProxyRenderer{},
		Health:   noopBackendHealthChecker{},
	}); err != nil {
		t.Fatalf("register backend failed: %v", err)
	}
	app := &application{home: tmpHome, cwd: tmpHome, username: "tester", registry: reg, backendRegistry: backendReg}
	ref := scope.MustRef("stub", "default")

	if err := app.cmdBootstrap([]string{
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--runtime-mode", "gateway",
		"--gateway-backend", "stub-backend",
	}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	if err := app.cmdProxy([]string{"install", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("proxy install failed: %v", err)
	}
	if err := app.cmdService([]string{"reconcile", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("service reconcile failed: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)
	st, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	if !st.Service.Running {
		t.Fatalf("expected running=true after service reconcile: %+v", st.Service)
	}
}

func TestServiceStartAutoRecoversViaReconcile(t *testing.T) {
	tmpHome := t.TempDir()
	stub := filepath.Join(tmpHome, "launchctl")
	marker := filepath.Join(tmpHome, "kickstart-sync.failed.once")
	stubScript := "#!/usr/bin/env bash\nset -euo pipefail\ncmd=\"${1:-}\"\nmarker=\"${CCB_TEST_RECOVER_MARKER:-/tmp/ccb-recover-marker}\"\nif [[ \"$cmd\" == \"kickstart\" ]]; then\n  target=\"${3:-}\"\n  if [[ \"$target\" == *\".sync\" ]] && [[ ! -f \"$marker\" ]]; then\n    touch \"$marker\"\n    printf 'Could not find service\\n' >&2\n    exit 113\n  fi\nfi\nif [[ \"$cmd\" == \"print\" ]]; then\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)
	t.Setenv("CCB_TEST_RECOVER_MARKER", marker)

	reg := provider.NewRegistry()
	if err := reg.Register(provider.Bundle{
		VendorID:     "stub",
		RuntimeModes: []string{"gateway"},
		Capabilities: map[provider.Capability]bool{
			provider.CapabilityAuth: true,
		},
		Auth: noopAuthStrategy{},
	}); err != nil {
		t.Fatalf("register provider failed: %v", err)
	}
	backendReg := backend.NewRegistry()
	if err := backendReg.Register(backend.Bundle{
		ID: "stub-backend",
		Capabilities: map[backend.Capability]bool{
			backend.CapabilityArtifact: true,
			backend.CapabilityProxy:    true,
			backend.CapabilityHealth:   true,
		},
		Artifact: fakeBackendArtifactInstaller{},
		Proxy:    noopBackendProxyRenderer{},
		Health:   noopBackendHealthChecker{},
	}); err != nil {
		t.Fatalf("register backend failed: %v", err)
	}
	app := &application{home: tmpHome, cwd: tmpHome, username: "tester", registry: reg, backendRegistry: backendReg}
	ref := scope.MustRef("stub", "default")

	if err := app.cmdBootstrap([]string{
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--runtime-mode", "gateway",
		"--gateway-backend", "stub-backend",
	}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	if err := app.cmdProxy([]string{"install", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("proxy install failed: %v", err)
	}
	if err := app.cmdService([]string{"install", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("service install failed: %v", err)
	}
	if err := app.cmdService([]string{"start", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("service start with auto-reconcile should succeed: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected marker file to confirm initial failure path: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)
	st, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	if !st.Service.Running {
		t.Fatalf("expected running=true after auto-recovered start: %+v", st.Service)
	}
}

func TestDoctorClearErrorHistoryFlag(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--runtime-mode", "gateway"}); err != nil {
		t.Fatalf("bootstrap gateway failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--runtime-mode", "native"}); err != nil {
		t.Fatalf("bootstrap native failed: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)
	logBody := "2026-02-14T00:00:00Z [INFO] setup complete\n2026-02-14T00:00:01Z [ERROR] code=ERR_TEST | simulated\n"
	if err := os.WriteFile(paths.AppLogPath, []byte(logBody), 0o644); err != nil {
		t.Fatalf("write app log failed: %v", err)
	}

	if err := app.cmdDoctor([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--clear-error-history"}); err != nil {
		t.Fatalf("doctor with clear-error-history failed: %v", err)
	}
	b, err := os.ReadFile(paths.AppLogPath)
	if err != nil {
		t.Fatalf("read app log failed: %v", err)
	}
	text := string(b)
	if strings.Contains(text, "[ERROR]") {
		t.Fatalf("expected error entries to be removed, got: %s", text)
	}
	if !strings.Contains(text, "[INFO]") {
		t.Fatalf("expected non-error entries to remain, got: %s", text)
	}
}

func TestServiceInstallRejectsNativeMode(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "default", "--runtime-mode", "gateway"}); err != nil {
		t.Fatalf("bootstrap gateway failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "default", "--runtime-mode", "native"}); err != nil {
		t.Fatalf("bootstrap native failed: %v", err)
	}

	err = app.cmdService([]string{"install", "--vendor", "codex", "--profile", "default"})
	if err == nil {
		t.Fatal("expected native mode service install rejection")
	}
	if code := cberr.Code(err); code != cberr.ErrCapabilityMissing {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "runtime_mode=gateway") {
		t.Fatalf("expected gateway-mode hint, got: %v", err)
	}
}

func TestBootstrapRejectsNativeForNewScope(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	err = app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "new-scope", "--runtime-mode", "native"})
	if err == nil {
		t.Fatal("expected native bootstrap rejection for new scope")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "cleanup-only") {
		t.Fatalf("expected cleanup-only hint, got: %v", err)
	}
}

func TestBootstrapRejectsUnknownGatewayBackend(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	err = app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "default", "--gateway-backend", "missing-backend"})
	if err == nil {
		t.Fatal("expected unknown backend rejection")
	}
	if code := cberr.Code(err); code != cberr.ErrBackendMissing {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestLoadRuntimeAllowsMissingBackendInNativeCleanupMode(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--runtime-mode", "gateway"}); err != nil {
		t.Fatalf("bootstrap gateway failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--runtime-mode", "native",
		"--gateway-backend", "missing-backend",
	}); err != nil {
		t.Fatalf("bootstrap native failed: %v", err)
	}
	rt, err := app.loadRuntime(ref, false)
	if err != nil {
		t.Fatalf("load runtime failed in native cleanup mode: %v", err)
	}
	if string(rt.Config.RuntimeMode) != string(config.RuntimeModeNativeCleanup) {
		t.Fatalf("expected runtime mode %s, got %q", config.RuntimeModeNativeCleanup, rt.Config.RuntimeMode)
	}
}

func TestServiceInvalidSubcommandDoesNotCreateScopeFiles(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	err = app.cmdService([]string{"foo", "--vendor", "codex", "--profile", "default"})
	if err == nil {
		t.Fatal("expected invalid args error")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}

	configPath := filepath.Join(tmpHome, ".ccgateway", "vendors", "codex", "profiles", "default", "config.yaml")
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("unexpected side effect: scope config created at %s", configPath)
	}
}

func TestClaudeInvalidSubcommandDoesNotCreateScopeFiles(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	err = app.cmdClaude([]string{"foo", "--vendor", "codex", "--profile", "default"})
	if err == nil {
		t.Fatal("expected invalid args error")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}

	configPath := filepath.Join(tmpHome, ".ccgateway", "vendors", "codex", "profiles", "default", "config.yaml")
	if _, statErr := os.Stat(configPath); !os.IsNotExist(statErr) {
		t.Fatalf("unexpected side effect: scope config created at %s", configPath)
	}
}

func TestAllowSettingsMutation(t *testing.T) {
	ref := scope.MustRef("codex", "default")
	tests := []struct {
		name       string
		active     control.ActivePointer
		generation string
		allowed    bool
	}{
		{
			name:       "legacy active generation empty with mismatched scope blocks",
			active:     control.ActivePointer{ActiveVendor: "other", ActiveProfile: "scope", ActiveGeneration: ""},
			generation: "gen-1",
			allowed:    false,
		},
		{
			name:       "legacy active generation empty with matching scope allows",
			active:     control.ActivePointer{ActiveVendor: "codex", ActiveProfile: "default", ActiveGeneration: ""},
			generation: "gen-1",
			allowed:    true,
		},
		{
			name:       "legacy active pointer unset allows",
			active:     control.ActivePointer{ActiveVendor: "", ActiveProfile: "", ActiveGeneration: ""},
			generation: "gen-1",
			allowed:    true,
		},
		{
			name:       "active generation set with matching scope and generation allows",
			active:     control.ActivePointer{ActiveVendor: "codex", ActiveProfile: "default", ActiveGeneration: "gen-1"},
			generation: "gen-1",
			allowed:    true,
		},
		{
			name:       "active generation set with mismatched scope blocks",
			active:     control.ActivePointer{ActiveVendor: "codex", ActiveProfile: "alt", ActiveGeneration: "gen-1"},
			generation: "gen-1",
			allowed:    false,
		},
		{
			name:       "active generation set with mismatched generation blocks",
			active:     control.ActivePointer{ActiveVendor: "codex", ActiveProfile: "default", ActiveGeneration: "gen-2"},
			generation: "gen-1",
			allowed:    false,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := allowSettingsMutation(tc.active, ref, tc.generation)
			if got != tc.allowed {
				t.Fatalf("allowSettingsMutation()=%t, want %t", got, tc.allowed)
			}
		})
	}
}

func TestClaudeRevertRejectsGenerationMismatch(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	ref := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)

	st, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	st.Claude.Applied = true
	st.Claude.SnapshotPath = filepath.Join(tmpHome, "missing-snapshot.json")
	st.Claude.SnapshotSHA256 = "deadbeef"
	st.Claude.AppliedGeneration = "gen-old"
	if err := state.Save(paths.StatePath, st); err != nil {
		t.Fatalf("save state failed: %v", err)
	}
	if err := control.SaveActive(paths.ActivePath, control.ActivePointer{
		ActiveVendor:     ref.VendorID,
		ActiveProfile:    ref.ProfileID,
		ActiveGeneration: "gen-new",
	}); err != nil {
		t.Fatalf("save active failed: %v", err)
	}

	err = app.cmdClaude([]string{"revert", "--vendor", ref.VendorID, "--profile", ref.ProfileID})
	if err == nil {
		t.Fatal("expected generation mismatch error")
	}
	if code := cberr.Code(err); code != cberr.ErrGenerationMismatch {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
	after, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state after revert failed: %v", err)
	}
	if !after.Claude.Applied {
		t.Fatalf("expected state to remain applied on blocked revert: %+v", after.Claude)
	}
}

func TestClaudeApplyUpdatesActiveGenerationWhenScopeIsActive(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	ref := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--runtime-mode", "gateway"}); err != nil {
		t.Fatalf("bootstrap gateway failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--runtime-mode", "native"}); err != nil {
		t.Fatalf("bootstrap native failed: %v", err)
	}
	if err := app.cmdClaude([]string{"apply", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("claude apply failed: %v", err)
	}

	paths := scope.BuildPaths(app.home, app.cwd, ref)
	active, err := control.LoadActive(paths.ActivePath)
	if err != nil {
		t.Fatalf("load active failed: %v", err)
	}
	st, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	if !st.Claude.Applied {
		t.Fatalf("expected applied state: %+v", st.Claude)
	}
	if st.Claude.AppliedGeneration == "" {
		t.Fatalf("expected non-empty applied generation: %+v", st.Claude)
	}
	if active.ActiveGeneration == "" {
		t.Fatalf("expected non-empty active generation: %+v", active)
	}
	if active.ActiveGeneration != st.Claude.AppliedGeneration {
		t.Fatalf("generation mismatch active=%s state=%s", active.ActiveGeneration, st.Claude.AppliedGeneration)
	}
}

func TestClaudeApplyDoesNotMutateActiveGenerationForInactiveScope(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	activeRef := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", activeRef.VendorID, "--profile", activeRef.ProfileID, "--runtime-mode", "gateway"}); err != nil {
		t.Fatalf("bootstrap active gateway scope failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", activeRef.VendorID, "--profile", activeRef.ProfileID, "--runtime-mode", "native"}); err != nil {
		t.Fatalf("bootstrap active native scope failed: %v", err)
	}
	inactiveRef := scope.MustRef("codex", "alt")
	if err := app.cmdBootstrap([]string{"--vendor", inactiveRef.VendorID, "--profile", inactiveRef.ProfileID, "--runtime-mode", "gateway"}); err != nil {
		t.Fatalf("bootstrap inactive gateway scope failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", inactiveRef.VendorID, "--profile", inactiveRef.ProfileID, "--runtime-mode", "native"}); err != nil {
		t.Fatalf("bootstrap inactive native scope failed: %v", err)
	}

	activePaths := scope.BuildPaths(app.home, app.cwd, activeRef)
	before, err := control.LoadActive(activePaths.ActivePath)
	if err != nil {
		t.Fatalf("load active before apply failed: %v", err)
	}
	if err := app.cmdClaude([]string{"apply", "--vendor", inactiveRef.VendorID, "--profile", inactiveRef.ProfileID}); err != nil {
		t.Fatalf("claude apply failed: %v", err)
	}
	after, err := control.LoadActive(activePaths.ActivePath)
	if err != nil {
		t.Fatalf("load active after apply failed: %v", err)
	}
	if after.ActiveVendor != before.ActiveVendor || after.ActiveProfile != before.ActiveProfile || after.ActiveGeneration != before.ActiveGeneration {
		t.Fatalf("inactive scope apply should not mutate active pointer: before=%+v after=%+v", before, after)
	}
}

func TestClaudeApplyRejectsProjectSettingsPathMismatch(t *testing.T) {
	tmpHome := t.TempDir()
	cwdA := filepath.Join(tmpHome, "project-a")
	cwdB := filepath.Join(tmpHome, "project-b")
	if err := os.MkdirAll(cwdA, 0o755); err != nil {
		t.Fatalf("mkdir project-a failed: %v", err)
	}
	if err := os.MkdirAll(cwdB, 0o755); err != nil {
		t.Fatalf("mkdir project-b failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", cwdA)

	appA, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication project-a failed: %v", err)
	}
	ref := scope.MustRef("claude", "default")
	if err := appA.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("bootstrap project-a failed: %v", err)
	}

	appB := &application{
		home:            tmpHome,
		cwd:             cwdB,
		username:        "tester",
		registry:        providers.DefaultRegistry(),
		backendRegistry: backends.DefaultRegistry(),
	}
	err = appB.cmdClaude([]string{"apply", "--vendor", ref.VendorID, "--profile", ref.ProfileID})
	if err == nil {
		t.Fatal("expected claude apply failure for settings path mismatch")
	}
	if code := cberr.Code(err); code != cberr.ErrConfigOverridden {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "--settings-layer project") {
		t.Fatalf("expected remediation hint, got: %v", err)
	}
}

func TestClaudeApplyPreservesOriginalSnapshotAcrossReapply(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	ref := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--runtime-mode", "gateway"}); err != nil {
		t.Fatalf("bootstrap gateway failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--runtime-mode", "native"}); err != nil {
		t.Fatalf("bootstrap native failed: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)
	if err := os.MkdirAll(filepath.Dir(paths.ClaudeUserSettingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir failed: %v", err)
	}
	original := []byte("{\n  \"model\": \"gpt-5.3-codex\",\n  \"env\": {\n    \"ANTHROPIC_BASE_URL\": \"http://127.0.0.1:8317\",\n    \"ANTHROPIC_AUTH_TOKEN\": \"ccb::codex::default::gen-1\",\n    \"ANTHROPIC_MODEL\": \"gpt-5.3-codex\",\n    \"custom\": \"keep\"\n  }\n}\n")
	if err := os.WriteFile(paths.ClaudeUserSettingsPath, original, 0o600); err != nil {
		t.Fatalf("write original settings failed: %v", err)
	}

	if err := app.cmdClaude([]string{"apply", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("first claude apply failed: %v", err)
	}
	first, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state after first apply failed: %v", err)
	}
	if first.Claude.SnapshotPath == "" || first.Claude.SnapshotSHA256 == "" {
		t.Fatalf("expected snapshot metadata after first apply: %+v", first.Claude)
	}

	if err := app.cmdClaude([]string{"apply", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("second claude apply failed: %v", err)
	}
	second, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state after second apply failed: %v", err)
	}
	if second.Claude.SnapshotPath != first.Claude.SnapshotPath {
		t.Fatalf("snapshot path should remain original across reapply: first=%s second=%s", first.Claude.SnapshotPath, second.Claude.SnapshotPath)
	}
	if second.Claude.SnapshotSHA256 != first.Claude.SnapshotSHA256 {
		t.Fatalf("snapshot hash should remain original across reapply: first=%s second=%s", first.Claude.SnapshotSHA256, second.Claude.SnapshotSHA256)
	}

	if err := app.cmdClaude([]string{"revert", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("claude revert failed: %v", err)
	}
	restored, err := os.ReadFile(paths.ClaudeUserSettingsPath)
	if err != nil {
		t.Fatalf("read restored settings failed: %v", err)
	}
	if string(restored) != string(original) {
		t.Fatalf("revert should restore original settings\nwant:\n%s\ngot:\n%s", string(original), string(restored))
	}
}

func TestUsePreservesOriginalSnapshotAcrossReapply(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	tmpCwd := filepath.Join(tmpHome, "workspace")
	if err := os.MkdirAll(tmpCwd, 0o755); err != nil {
		t.Fatalf("mkdir workspace failed: %v", err)
	}

	backendReg := backend.NewRegistry()
	if err := backendReg.Register(backend.Bundle{
		ID: "stub-backend",
		Capabilities: map[backend.Capability]bool{
			backend.CapabilityProxy:  true,
			backend.CapabilityHealth: true,
		},
		Proxy:  noopBackendProxyRenderer{},
		Health: noopBackendHealthChecker{},
	}); err != nil {
		t.Fatalf("register backend failed: %v", err)
	}
	app := &application{
		home:            tmpHome,
		cwd:             tmpCwd,
		username:        "tester",
		registry:        providers.DefaultRegistry(),
		backendRegistry: backendReg,
	}

	ref := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--gateway-backend", "stub-backend"}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)
	if err := os.MkdirAll(filepath.Dir(paths.ClaudeUserSettingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir failed: %v", err)
	}
	original := []byte("{\n  \"model\": \"pre-existing\",\n  \"env\": {\n    \"ANTHROPIC_BASE_URL\": \"https://example.invalid\",\n    \"custom\": \"keep\"\n  }\n}\n")
	if err := os.WriteFile(paths.ClaudeUserSettingsPath, original, 0o600); err != nil {
		t.Fatalf("write original settings failed: %v", err)
	}

	if err := app.cmdUse([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("first use failed: %v", err)
	}
	first, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state after first use failed: %v", err)
	}
	if first.Claude.SnapshotPath == "" || first.Claude.SnapshotSHA256 == "" {
		t.Fatalf("expected snapshot metadata after first use: %+v", first.Claude)
	}

	if err := app.cmdUse([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("second use failed: %v", err)
	}
	second, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state after second use failed: %v", err)
	}
	if second.Claude.SnapshotPath != first.Claude.SnapshotPath {
		t.Fatalf("snapshot path should remain original across repeated use: first=%s second=%s", first.Claude.SnapshotPath, second.Claude.SnapshotPath)
	}
	if second.Claude.SnapshotSHA256 != first.Claude.SnapshotSHA256 {
		t.Fatalf("snapshot hash should remain original across repeated use: first=%s second=%s", first.Claude.SnapshotSHA256, second.Claude.SnapshotSHA256)
	}

	if err := app.cmdClaude([]string{"revert", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("claude revert failed: %v", err)
	}
	restored, err := os.ReadFile(paths.ClaudeUserSettingsPath)
	if err != nil {
		t.Fatalf("read restored settings failed: %v", err)
	}
	if string(restored) != string(original) {
		t.Fatalf("revert should restore original settings\nwant:\n%s\ngot:\n%s", string(original), string(restored))
	}
}

func TestUninstallBlocksUnsafeClaudeRevertForActiveScope(t *testing.T) {
	tmpHome := t.TempDir()
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nprintf 'Could not find service\\n' >&2\nexit 1\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}

	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	ref := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)

	st, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	st.Claude.Applied = true
	st.Claude.SnapshotPath = filepath.Join(tmpHome, "missing-snapshot.json")
	st.Claude.SnapshotSHA256 = "deadbeef"
	st.Claude.AppliedGeneration = "gen-old"
	if err := state.Save(paths.StatePath, st); err != nil {
		t.Fatalf("save state failed: %v", err)
	}
	if err := control.SaveActive(paths.ActivePath, control.ActivePointer{
		ActiveVendor:     ref.VendorID,
		ActiveProfile:    ref.ProfileID,
		ActiveGeneration: "gen-new",
	}); err != nil {
		t.Fatalf("save active failed: %v", err)
	}

	err = app.cmdUninstall([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID})
	if err == nil {
		t.Fatal("expected uninstall failure on active generation mismatch")
	}
	if code := cberr.Code(err); code != cberr.ErrGenerationMismatch {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
	after, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state after uninstall failed: %v", err)
	}
	if !after.Claude.Applied || after.Claude.SnapshotPath == "" || after.Claude.SnapshotSHA256 == "" || after.Claude.AppliedGeneration == "" {
		t.Fatalf("expected claude state to remain intact on blocked uninstall: %+v", after.Claude)
	}
}

func TestUninstallSkipsUnsafeClaudeRevertForInactiveScope(t *testing.T) {
	tmpHome := t.TempDir()
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nprintf 'Could not find service\\n' >&2\nexit 1\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}

	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	ref := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)

	st, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	st.Claude.Applied = true
	st.Claude.SnapshotPath = filepath.Join(tmpHome, "missing-snapshot.json")
	st.Claude.SnapshotSHA256 = "deadbeef"
	st.Claude.AppliedGeneration = "gen-old"
	if err := state.Save(paths.StatePath, st); err != nil {
		t.Fatalf("save state failed: %v", err)
	}
	if err := control.SaveActive(paths.ActivePath, control.ActivePointer{
		ActiveVendor:     "codex",
		ActiveProfile:    "other",
		ActiveGeneration: "gen-new",
	}); err != nil {
		t.Fatalf("save active failed: %v", err)
	}

	if err := app.cmdUninstall([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}
	after, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state after uninstall failed: %v", err)
	}
	if after.Claude.Applied || after.Claude.SnapshotPath != "" || after.Claude.SnapshotSHA256 != "" || after.Claude.AppliedGeneration != "" {
		t.Fatalf("expected claude state to be cleared for inactive scope uninstall: %+v", after.Claude)
	}
}

func TestServiceStartRejectsBackendWithoutHealthCapability(t *testing.T) {
	tmpHome := t.TempDir()
	reg := provider.NewRegistry()
	if err := reg.Register(provider.Bundle{
		VendorID:     "stub",
		RuntimeModes: []string{"gateway"},
	}); err != nil {
		t.Fatalf("register provider failed: %v", err)
	}
	backendReg := backend.NewRegistry()
	if err := backendReg.Register(backend.Bundle{
		ID: "stub-backend",
		Capabilities: map[backend.Capability]bool{
			backend.CapabilityProxy: true,
		},
		Proxy: noopBackendProxyRenderer{},
	}); err != nil {
		t.Fatalf("register backend failed: %v", err)
	}
	app := &application{home: tmpHome, cwd: tmpHome, username: "tester", registry: reg, backendRegistry: backendReg}

	if err := app.cmdBootstrap([]string{"--vendor", "stub", "--profile", "default", "--gateway-backend", "stub-backend"}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	err := app.cmdService([]string{"start", "--vendor", "stub", "--profile", "default"})
	if err == nil {
		t.Fatal("expected missing health capability error")
	}
	if code := cberr.Code(err); code != cberr.ErrCapabilityMissing {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestServiceStatusRejectsBackendWithoutHealthImplementation(t *testing.T) {
	tmpHome := t.TempDir()
	reg := provider.NewRegistry()
	if err := reg.Register(provider.Bundle{
		VendorID:     "stub",
		RuntimeModes: []string{"gateway"},
	}); err != nil {
		t.Fatalf("register provider failed: %v", err)
	}
	backendReg := backend.NewRegistry()
	if err := backendReg.Register(backend.Bundle{
		ID: "stub-backend",
		Capabilities: map[backend.Capability]bool{
			backend.CapabilityProxy:  true,
			backend.CapabilityHealth: true,
		},
		Proxy: noopBackendProxyRenderer{},
		// Intentionally omit Health implementation to validate backend runtime guard.
	}); err != nil {
		t.Fatalf("register backend failed: %v", err)
	}
	app := &application{home: tmpHome, cwd: tmpHome, username: "tester", registry: reg, backendRegistry: backendReg}

	if err := app.cmdBootstrap([]string{"--vendor", "stub", "--profile", "default", "--gateway-backend", "stub-backend"}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	err := app.cmdService([]string{"status", "--vendor", "stub", "--profile", "default"})
	if err == nil {
		t.Fatal("expected missing health implementation error")
	}
	if code := cberr.Code(err); code != cberr.ErrCapabilityMissing {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestServiceStartRetriesHealthCheckUntilReady(t *testing.T) {
	tmpHome := t.TempDir()
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nset -euo pipefail\ncmd=\"${1:-}\"\nif [[ \"$cmd\" == \"print\" ]]; then\n  printf 'Could not find service\\n' >&2\n  exit 1\nfi\nexit 0\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)

	reg := provider.NewRegistry()
	if err := reg.Register(provider.Bundle{
		VendorID:     "stub",
		RuntimeModes: []string{"gateway"},
	}); err != nil {
		t.Fatalf("register provider failed: %v", err)
	}
	flaky := &flakyBackendHealthChecker{failuresRemaining: 3}
	backendReg := backend.NewRegistry()
	if err := backendReg.Register(backend.Bundle{
		ID: "stub-backend",
		Capabilities: map[backend.Capability]bool{
			backend.CapabilityProxy:  true,
			backend.CapabilityHealth: true,
		},
		Proxy:  noopBackendProxyRenderer{},
		Health: flaky,
	}); err != nil {
		t.Fatalf("register backend failed: %v", err)
	}
	app := &application{home: tmpHome, cwd: tmpHome, username: "tester", registry: reg, backendRegistry: backendReg}
	ref := scope.MustRef("stub", "default")
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--gateway-backend", "stub-backend"}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)
	if err := os.WriteFile(paths.ProxyBinary, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write proxy binary failed: %v", err)
	}
	if err := app.cmdService([]string{"install", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("service install failed: %v", err)
	}

	startedAt := time.Now()
	if err := app.cmdService([]string{"start", "--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("service start should succeed after health retries: %v", err)
	}
	elapsed := time.Since(startedAt)
	if got := flaky.Calls(); got < 4 {
		t.Fatalf("expected multiple health attempts, got %d", got)
	}
	if elapsed < 500*time.Millisecond {
		t.Fatalf("expected retry delay before success, elapsed=%s", elapsed)
	}
}

func TestUseRejectsNativeCleanupScope(t *testing.T) {
	tmpHome := t.TempDir()
	reg := provider.NewRegistry()
	if err := reg.Register(provider.Bundle{
		VendorID:     "stub",
		RuntimeModes: []string{string(config.RuntimeModeGateway), string(config.RuntimeModeNativeCleanup)},
		Capabilities: map[provider.Capability]bool{
			provider.CapabilityClaude: true,
		},
		Claude: noopClaudePatcher{},
	}); err != nil {
		t.Fatalf("register provider failed: %v", err)
	}
	app := &application{home: tmpHome, cwd: tmpHome, username: "tester", registry: reg}

	if err := app.cmdBootstrap([]string{"--vendor", "stub", "--profile", "default", "--runtime-mode", "gateway"}); err != nil {
		t.Fatalf("bootstrap gateway failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", "stub", "--profile", "default", "--runtime-mode", "native"}); err != nil {
		t.Fatalf("bootstrap native failed: %v", err)
	}
	err := app.cmdUse([]string{"--vendor", "stub", "--profile", "default"})
	if err == nil {
		t.Fatal("expected native cleanup scope to be rejected by use")
	}
	if code := cberr.Code(err); code != cberr.ErrCapabilityMissing {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
	if !strings.Contains(err.Error(), "cleanup-only") {
		t.Fatalf("expected cleanup-only hint, got: %v", err)
	}
}

func TestBootstrapReallocatesBusyPortWhenLaunchdServiceMissing(t *testing.T) {
	tmpHome := t.TempDir()
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nprintf 'Could not find service\\n' >&2\nexit 1\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer ln.Close()
	busyPort := ln.Addr().(*net.TCPAddr).Port
	server := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/models" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("{\"object\":\"list\",\"data\":[]}"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}),
	}
	go func() {
		_ = server.Serve(ln)
	}()
	defer server.Close()

	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(app.home, app.cwd, ref)
	if err := control.SavePorts(paths.PortsPath, control.PortRegistry{
		SchemaVersion: 1,
		Scopes: map[string]int{
			ref.ScopeID(): busyPort,
		},
	}); err != nil {
		t.Fatalf("save ports failed: %v", err)
	}

	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	rt, err := app.loadRuntime(ref, false)
	if err != nil {
		t.Fatalf("load runtime failed: %v", err)
	}
	if rt.Config.Port == busyPort {
		t.Fatalf("expected port reallocation away from busy non-ccgateway service, got %d", rt.Config.Port)
	}
}

func TestUninstallPurgeFailsWhenActivePointerUnreadable(t *testing.T) {
	tmpHome := t.TempDir()
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nprintf 'Could not find service\\n' >&2\nexit 1\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	ref := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)
	if err := os.WriteFile(paths.ActivePath, []byte("{bad-json"), 0o644); err != nil {
		t.Fatalf("write malformed active pointer failed: %v", err)
	}

	err = app.cmdUninstall([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--purge"})
	if err == nil {
		t.Fatal("expected purge failure on unreadable active pointer")
	}
	if code := cberr.Code(err); code != cberr.ErrSwitchLockFailed {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
	if _, statErr := os.Stat(paths.ScopeDir); statErr != nil {
		t.Fatalf("scope dir should not be removed on failed purge: %v", statErr)
	}
}

func TestBootstrapNativeSkipsPortRegistryAllocation(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--runtime-mode", "gateway"}); err != nil {
		t.Fatalf("bootstrap gateway failed: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)
	if err := os.WriteFile(paths.PortsPath, []byte("{bad-json"), 0o644); err != nil {
		t.Fatalf("write malformed ports registry failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID, "--runtime-mode", "native"}); err != nil {
		t.Fatalf("bootstrap native should not require ports registry parsing: %v", err)
	}
}

func TestUninstallUsesStoredServiceLabels(t *testing.T) {
	tmpHome := t.TempDir()
	logFile := filepath.Join(tmpHome, "launchctl.log")
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nprintf '%s\\n' \"$*\" >> \"$CCB_TEST_LAUNCHCTL_LOG\"\nprintf 'Could not find service\\n' >&2\nexit 1\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)
	t.Setenv("CCB_TEST_LAUNCHCTL_LOG", logFile)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)
	st, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state failed: %v", err)
	}
	st.Service.ProxyLabel = "com.custom.proxy"
	st.Service.SyncLabel = "com.custom.sync"
	if err := state.Save(paths.StatePath, st); err != nil {
		t.Fatalf("save state failed: %v", err)
	}
	if err := app.cmdUninstall([]string{"--vendor", ref.VendorID, "--profile", ref.ProfileID}); err != nil {
		t.Fatalf("uninstall failed: %v", err)
	}
	logs, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read launchctl log failed: %v", err)
	}
	text := string(logs)
	if !strings.Contains(text, "gui/") || !strings.Contains(text, "com.custom.proxy") || !strings.Contains(text, "com.custom.sync") {
		t.Fatalf("expected stored labels in launchctl calls, got: %s", text)
	}
}

func TestLogFailureWritesScopeErrorLog(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	args := []string{"proxy", "install", "--vendor", ref.VendorID, "--profile", ref.ProfileID}
	app.logFailure(args, cberr.New(cberr.ErrInvalidArgs, "bad args"))

	paths := scope.BuildPaths(app.home, app.cwd, ref)
	b, err := os.ReadFile(paths.AppLogPath)
	if err != nil {
		t.Fatalf("read scope app log failed: %v", err)
	}
	text := string(b)
	if !strings.Contains(text, "[ERROR]") {
		t.Fatalf("expected error log entry, got: %s", text)
	}
	if !strings.Contains(text, cberr.ErrInvalidArgs) {
		t.Fatalf("expected error code in log entry, got: %s", text)
	}
}

func TestCurrentUsernameFallbackMatchesScopeCurrentUserFallback(t *testing.T) {
	t.Setenv("USER", "")
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(app.home, app.cwd, ref)
	proxyLabel, _ := ref.Labels(app.username)
	if !strings.HasSuffix(paths.ProxyPlistPath, proxyLabel+".plist") {
		t.Fatalf("launchd label mismatch: path=%s label=%s", paths.ProxyPlistPath, proxyLabel)
	}
}

func TestStatusRejectsInvalidSinceDuration(t *testing.T) {
	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	err = app.cmdStatus([]string{"--vendor", "codex", "--profile", "default", "--since", "not-a-duration"})
	if err == nil {
		t.Fatal("expected invalid duration error")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}
}

func TestParseProxyLogTrafficLine(t *testing.T) {
	line := "[2026-02-15 00:29:24] [55576604] [info ] [gin_logger.go:93] 200 |        5.767s |       127.0.0.1 | POST    \"/v1/messages?beta=true\""
	ts, path, ok := parseProxyLogTrafficLine(line)
	if !ok {
		t.Fatal("expected line to parse")
	}
	if ts.Year() != 2026 || ts.Month() != 2 || ts.Day() != 15 {
		t.Fatalf("unexpected parsed timestamp: %v", ts)
	}
	if path != "/v1/messages?beta=true" {
		t.Fatalf("unexpected parsed path: %s", path)
	}
	if _, _, ok := parseProxyLogTrafficLine("not-a-log-line"); ok {
		t.Fatal("expected invalid line parse failure")
	}
}

func TestSummarizeProxyLogCountsWindowedTraffic(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "proxy.log")
	body := strings.Join([]string{
		"[2026-02-15 00:10:00] [a1] [info ] [gin_logger.go:93] 200 |        1.000s |       127.0.0.1 | POST    \"/v1/messages?beta=true\"",
		"[2026-02-15 00:11:00] [a2] [info ] [gin_logger.go:93] 200 |        0.050s |       127.0.0.1 | POST    \"/v1/messages/count_tokens?beta=true\"",
		"[2026-02-15 00:12:00] [a3] [info ] [gin_logger.go:93] 200 |        0.001s |       127.0.0.1 | GET     \"/v1/models\"",
		"[2026-02-14 20:00:00] [a4] [info ] [gin_logger.go:93] 200 |        1.500s |       127.0.0.1 | POST    \"/v1/chat/completions\"",
		"",
	}, "\n")
	if err := os.WriteFile(logPath, []byte(body), 0o644); err != nil {
		t.Fatalf("write proxy log failed: %v", err)
	}

	now := time.Date(2026, 2, 15, 1, 0, 0, 0, time.Local)
	sum, err := summarizeProxyLog(logPath, 2*time.Hour, now)
	if err != nil {
		t.Fatalf("summarizeProxyLog failed: %v", err)
	}
	if sum.Total != 3 {
		t.Fatalf("unexpected total: %d", sum.Total)
	}
	if sum.Chat != 1 {
		t.Fatalf("unexpected chat count: %d", sum.Chat)
	}
	if sum.CountTokens != 1 {
		t.Fatalf("unexpected count_tokens count: %d", sum.CountTokens)
	}
	if got := sum.ByPath["/v1/messages"]; got != 1 {
		t.Fatalf("unexpected /v1/messages count: %d", got)
	}
	if got := sum.ByPath["/v1/messages/count_tokens"]; got != 1 {
		t.Fatalf("unexpected /v1/messages/count_tokens count: %d", got)
	}
	if got := sum.ByPath["/v1/models"]; got != 1 {
		t.Fatalf("unexpected /v1/models count: %d", got)
	}
}

func TestModelSwitchSuccessAppliesSparkModel(t *testing.T) {
	tmpHome := t.TempDir()
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nexit 0\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)

	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate free port failed: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	if err := app.cmdBootstrap([]string{
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--port", strconv.Itoa(port),
	}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)
	if err := os.WriteFile(paths.ProxyBinary, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write proxy binary failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.AuthSource), 0o755); err != nil {
		t.Fatalf("mkdir auth source dir failed: %v", err)
	}
	if err := os.WriteFile(paths.AuthSource, []byte("{\"tokens\":{\"access_token\":\"test\"}}\n"), 0o600); err != nil {
		t.Fatalf("write auth source failed: %v", err)
	}

	rt, err := app.loadRuntime(ref, false)
	if err != nil {
		t.Fatalf("load runtime failed: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(rt.Config.Port))
	if err != nil {
		t.Fatalf("listen on runtime port failed: %v", err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/models" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("{\"object\":\"list\",\"data\":[]}"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}),
	}
	go func() {
		_ = srv.Serve(ln)
	}()
	defer func() {
		_ = srv.Close()
	}()

	if err := app.cmdModel([]string{
		"switch",
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--model", "codex-spark",
	}); err != nil {
		t.Fatalf("model switch failed: %v", err)
	}

	rt, err = app.loadRuntime(ref, false)
	if err != nil {
		t.Fatalf("load runtime after switch failed: %v", err)
	}
	if rt.Config.Model != "gpt-5.3-codex-spark" {
		t.Fatalf("expected spark model in config, got %q", rt.Config.Model)
	}
	if !rt.State.Service.Running {
		t.Fatalf("expected service running=true after model switch: %+v", rt.State.Service)
	}
	if rt.State.Claude.AppliedGeneration == "" {
		t.Fatalf("expected non-empty applied generation after model switch: %+v", rt.State.Claude)
	}
	settingsBody, err := os.ReadFile(paths.ClaudeUserSettingsPath)
	if err != nil {
		t.Fatalf("read settings failed: %v", err)
	}
	if !strings.Contains(string(settingsBody), "\"model\": \"gpt-5.3-codex-spark\"") {
		t.Fatalf("expected spark model in settings, got:\n%s", string(settingsBody))
	}
	proxyCfg, err := os.ReadFile(paths.ProxyConfig)
	if err != nil {
		t.Fatalf("read proxy config failed: %v", err)
	}
	if !strings.Contains(string(proxyCfg), "name: \"gpt-5.3-codex-spark\"") {
		t.Fatalf("expected spark model aliases in proxy config, got:\n%s", string(proxyCfg))
	}
}

func TestModelSwitchRollsBackConfigStateAndSettingsOnDoctorFailure(t *testing.T) {
	tmpHome := t.TempDir()
	tmpCwd := filepath.Join(tmpHome, "workspace")
	if err := os.MkdirAll(tmpCwd, 0o755); err != nil {
		t.Fatalf("mkdir workspace failed: %v", err)
	}
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nexit 0\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)

	backendReg := backend.NewRegistry()
	if err := backendReg.Register(backend.Bundle{
		ID: "stub-backend",
		Capabilities: map[backend.Capability]bool{
			backend.CapabilityProxy:  true,
			backend.CapabilityHealth: true,
		},
		Proxy:  noopBackendProxyRenderer{},
		Health: noopBackendHealthChecker{},
	}); err != nil {
		t.Fatalf("register backend failed: %v", err)
	}
	app := &application{
		home:            tmpHome,
		cwd:             tmpCwd,
		username:        "tester",
		registry:        providers.DefaultRegistry(),
		backendRegistry: backendReg,
	}
	ref := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--gateway-backend", "stub-backend",
	}); err != nil {
		t.Fatalf("bootstrap failed: %v", err)
	}
	paths := scope.BuildPaths(app.home, app.cwd, ref)
	if err := os.WriteFile(paths.ProxyBinary, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write proxy binary failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.ClaudeUserSettingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir failed: %v", err)
	}
	originalSettings := []byte("{\n  \"model\": \"gpt-5.3-codex\",\n  \"env\": {\n    \"custom\": \"keep\"\n  }\n}\n")
	if err := os.WriteFile(paths.ClaudeUserSettingsPath, originalSettings, 0o600); err != nil {
		t.Fatalf("write original settings failed: %v", err)
	}

	beforeRT, err := app.loadRuntime(ref, false)
	if err != nil {
		t.Fatalf("load runtime before switch failed: %v", err)
	}
	beforeState, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state before switch failed: %v", err)
	}
	beforeActive, err := control.LoadActive(paths.ActivePath)
	if err != nil {
		t.Fatalf("load active before switch failed: %v", err)
	}

	err = app.cmdModel([]string{
		"switch",
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--model", "codex-spark",
	})
	if err == nil {
		t.Fatal("expected model switch failure due doctor health check")
	}
	if code := cberr.Code(err); code != cberr.ErrDoctorFailed && code != cberr.ErrRollbackFailed {
		t.Fatalf("unexpected error code: %s (%v)", code, err)
	}

	afterRT, err := app.loadRuntime(ref, false)
	if err != nil {
		t.Fatalf("load runtime after switch failed: %v", err)
	}
	if afterRT.Config.Model != beforeRT.Config.Model {
		t.Fatalf("config model should be restored on failure: before=%q after=%q", beforeRT.Config.Model, afterRT.Config.Model)
	}
	afterState, err := state.Load(paths.StatePath)
	if err != nil {
		t.Fatalf("load state after switch failed: %v", err)
	}
	if afterState.Claude != beforeState.Claude {
		t.Fatalf("claude state should be restored: before=%+v after=%+v", beforeState.Claude, afterState.Claude)
	}
	if afterState.Service.Running != beforeState.Service.Running {
		t.Fatalf("service running state should be restored: before=%t after=%t", beforeState.Service.Running, afterState.Service.Running)
	}
	afterActive, err := control.LoadActive(paths.ActivePath)
	if err != nil {
		t.Fatalf("load active after switch failed: %v", err)
	}
	if afterActive.ActiveVendor != beforeActive.ActiveVendor ||
		afterActive.ActiveProfile != beforeActive.ActiveProfile ||
		afterActive.ActiveGeneration != beforeActive.ActiveGeneration {
		t.Fatalf("active pointer should be restored: before=%+v after=%+v", beforeActive, afterActive)
	}
	restoredSettings, err := os.ReadFile(paths.ClaudeUserSettingsPath)
	if err != nil {
		t.Fatalf("read restored settings failed: %v", err)
	}
	if string(restoredSettings) != string(originalSettings) {
		t.Fatalf("settings should be restored on failed switch\nwant:\n%s\ngot:\n%s", string(originalSettings), string(restoredSettings))
	}
}

func TestUseRejectsExpectedActiveMismatch(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)

	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{
		"--vendor", "claude",
		"--profile", "source",
		"--runtime-mode", string(config.RuntimeModeNativeDirect),
		"--model", "claude-opus-4-6",
	}); err != nil {
		t.Fatalf("bootstrap source failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{
		"--vendor", "claude",
		"--profile", "target",
		"--runtime-mode", string(config.RuntimeModeNativeDirect),
		"--model", "claude-sonnet-4-6",
	}); err != nil {
		t.Fatalf("bootstrap target failed: %v", err)
	}
	if err := app.cmdUse([]string{"--vendor", "claude", "--profile", "source"}); err != nil {
		t.Fatalf("initial use source failed: %v", err)
	}

	err = app.cmdUseWithExpected([]string{"--vendor", "claude", "--profile", "target"}, &expectedActiveRequirement{
		VendorID:  "claude",
		ProfileID: "different",
	})
	if err == nil {
		t.Fatal("expected use failure for expected-active mismatch")
	}
	if code := cberr.Code(err); code != cberr.ErrSwitchValidation {
		t.Fatalf("unexpected use error code: %s (%v)", code, err)
	}
}

func TestModelSwitchConcurrentActiveChangeFailsWithoutClobberingActive(t *testing.T) {
	tmpHome := t.TempDir()
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nset -euo pipefail\nif [[ \"${1:-}\" == \"kickstart\" ]]; then\n  sleep 0.25\nfi\nif [[ \"${1:-}\" == \"print\" ]]; then\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)

	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate free port failed: %v", err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	if err := app.cmdBootstrap([]string{
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--port", strconv.Itoa(port),
	}); err != nil {
		t.Fatalf("bootstrap codex failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{
		"--vendor", "claude",
		"--profile", "alt",
		"--runtime-mode", string(config.RuntimeModeNativeDirect),
		"--model", "claude-opus-4-6",
	}); err != nil {
		t.Fatalf("bootstrap alt scope failed: %v", err)
	}

	paths := scope.BuildPaths(app.home, app.cwd, ref)
	if err := os.WriteFile(paths.ProxyBinary, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write proxy binary failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(paths.AuthSource), 0o755); err != nil {
		t.Fatalf("mkdir auth source dir failed: %v", err)
	}
	if err := os.WriteFile(paths.AuthSource, []byte("{\"tokens\":{\"access_token\":\"test\"}}\n"), 0o600); err != nil {
		t.Fatalf("write auth source failed: %v", err)
	}

	rt, err := app.loadRuntime(ref, false)
	if err != nil {
		t.Fatalf("load runtime failed: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(rt.Config.Port))
	if err != nil {
		t.Fatalf("listen on runtime port failed: %v", err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1/models" {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("{\"object\":\"list\",\"data\":[]}"))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}),
	}
	go func() {
		_ = srv.Serve(ln)
	}()
	defer func() {
		_ = srv.Close()
	}()

	switchErrCh := make(chan error, 1)
	go func() {
		time.Sleep(60 * time.Millisecond)
		other, err := newApplication()
		if err != nil {
			switchErrCh <- err
			return
		}
		switchErrCh <- other.cmdUse([]string{"--vendor", "claude", "--profile", "alt"})
	}()

	err = app.cmdModel([]string{
		"switch",
		"--vendor", ref.VendorID,
		"--profile", ref.ProfileID,
		"--model", "codex-spark",
	})
	if err == nil {
		t.Fatal("expected model switch failure under concurrent active change")
	}
	if code := cberr.Code(err); code != cberr.ErrSwitchValidation && code != cberr.ErrRollbackFailed {
		t.Fatalf("unexpected model switch error code: %s (%v)", code, err)
	}
	if switchErr := <-switchErrCh; switchErr != nil {
		t.Fatalf("concurrent active switch failed: %v", switchErr)
	}

	active, err := control.LoadActive(paths.ActivePath)
	if err != nil {
		t.Fatalf("load active failed: %v", err)
	}
	if active.ActiveVendor != "claude" || active.ActiveProfile != "alt" {
		t.Fatalf("expected concurrent active scope to remain (claude:alt), got %+v", active)
	}
	post, err := app.loadRuntime(ref, false)
	if err != nil {
		t.Fatalf("load runtime after model switch failure failed: %v", err)
	}
	if post.Config.Model != "gpt-5.3-codex" {
		t.Fatalf("expected rollback to keep original model, got %q", post.Config.Model)
	}
}

func TestFailoverConcurrentSourceChangeFailsWithoutClobberingActive(t *testing.T) {
	tmpHome := t.TempDir()
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nset -euo pipefail\nif [[ \"${1:-}\" == \"kickstart\" ]]; then\n  sleep 0.25\nfi\nif [[ \"${1:-}\" == \"print\" ]]; then\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)

	authSource := filepath.Join(tmpHome, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authSource), 0o755); err != nil {
		t.Fatalf("mkdir auth source dir failed: %v", err)
	}
	if err := os.WriteFile(authSource, []byte("{\"token\":\"ok\"}\n"), 0o600); err != nil {
		t.Fatalf("write auth source failed: %v", err)
	}

	backendReg := backend.NewRegistry()
	if err := backendReg.Register(backend.Bundle{
		ID: config.DefaultGatewayBackend,
		Capabilities: map[backend.Capability]bool{
			backend.CapabilityArtifact: true,
			backend.CapabilityProxy:    true,
			backend.CapabilityHealth:   true,
		},
		Artifact: fakeBackendArtifactInstaller{},
		Proxy:    noopBackendProxyRenderer{},
		Health:   noopBackendHealthChecker{},
	}); err != nil {
		t.Fatalf("register backend failed: %v", err)
	}

	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	app.backendRegistry = backendReg

	if err := app.cmdBootstrap([]string{
		"--vendor", "claude",
		"--profile", "source",
		"--runtime-mode", string(config.RuntimeModeNativeDirect),
		"--model", "claude-opus-4-6",
	}); err != nil {
		t.Fatalf("bootstrap source failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{
		"--vendor", "claude",
		"--profile", "alt",
		"--runtime-mode", string(config.RuntimeModeNativeDirect),
		"--model", "claude-sonnet-4-6",
	}); err != nil {
		t.Fatalf("bootstrap alt failed: %v", err)
	}
	if err := app.cmdUse([]string{"--vendor", "claude", "--profile", "source"}); err != nil {
		t.Fatalf("use source failed: %v", err)
	}

	paths := scope.BuildPaths(app.home, app.cwd, scope.MustRef("claude", "source"))
	switchErrCh := make(chan error, 1)
	go func() {
		time.Sleep(60 * time.Millisecond)
		other, err := newApplication()
		if err != nil {
			switchErrCh <- err
			return
		}
		switchErrCh <- other.cmdUse([]string{"--vendor", "claude", "--profile", "alt"})
	}()

	err = app.cmdFailover([]string{
		"--from", "claude:source",
		"--to", "codex:target",
		"--model", "gpt-5.3-codex",
	})
	if err == nil {
		t.Fatal("expected failover failure under concurrent source active change")
	}
	if code := cberr.Code(err); code != cberr.ErrSwitchFailed && code != cberr.ErrRollbackFailed {
		t.Fatalf("unexpected failover error code: %s (%v)", code, err)
	}
	if switchErr := <-switchErrCh; switchErr != nil {
		t.Fatalf("concurrent source switch failed: %v", switchErr)
	}

	active, err := control.LoadActive(paths.ActivePath)
	if err != nil {
		t.Fatalf("load active failed: %v", err)
	}
	if active.ActiveVendor != "claude" || active.ActiveProfile != "alt" {
		t.Fatalf("expected concurrent active scope to remain (claude:alt), got %+v", active)
	}
}

func TestUseAllowsNativeDirectScope(t *testing.T) {
	tmpHome := t.TempDir()
	reg := provider.NewRegistry()
	if err := reg.Register(provider.Bundle{
		VendorID:     "stub",
		RuntimeModes: []string{string(config.RuntimeModeGateway), string(config.RuntimeModeNativeDirect)},
		Capabilities: map[provider.Capability]bool{
			provider.CapabilityClaude: true,
		},
		Claude: noopClaudePatcher{},
	}); err != nil {
		t.Fatalf("register provider failed: %v", err)
	}
	app := &application{home: tmpHome, cwd: tmpHome, username: "tester", registry: reg}

	if err := app.cmdBootstrap([]string{
		"--vendor", "stub",
		"--profile", "default",
		"--runtime-mode", string(config.RuntimeModeNativeDirect),
		"--model", "claude-opus-4-6",
	}); err != nil {
		t.Fatalf("bootstrap native-direct failed: %v", err)
	}
	if err := app.cmdUse([]string{"--vendor", "stub", "--profile", "default"}); err != nil {
		t.Fatalf("use should allow native-direct scope: %v", err)
	}
}

func TestFailoverCodexToClaudeNativeDirectSuccess(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)

	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	fromRef := scope.MustRef("codex", "default")
	if err := app.cmdBootstrap([]string{"--vendor", fromRef.VendorID, "--profile", fromRef.ProfileID}); err != nil {
		t.Fatalf("bootstrap source failed: %v", err)
	}

	if err := app.cmdFailover([]string{
		"--from", "codex:default",
		"--to", "claude:default",
		"--model", "claude-opus-4-6",
	}); err != nil {
		t.Fatalf("failover failed: %v", err)
	}

	paths := scope.BuildPaths(tmpHome, tmpHome, fromRef)
	active, err := control.LoadActive(paths.ActivePath)
	if err != nil {
		t.Fatalf("load active failed: %v", err)
	}
	if active.ActiveVendor != "claude" || active.ActiveProfile != "default" {
		t.Fatalf("unexpected active scope after failover: %+v", active)
	}
	if strings.TrimSpace(active.ActiveGeneration) == "" {
		t.Fatalf("expected non-empty active generation after failover: %+v", active)
	}

	targetRef := scope.MustRef("claude", "default")
	targetRT, err := app.loadRuntime(targetRef, false)
	if err != nil {
		t.Fatalf("load target runtime failed: %v", err)
	}
	if targetRT.Config.RuntimeMode != config.RuntimeModeNativeDirect {
		t.Fatalf("expected runtime_mode=%s, got %s", config.RuntimeModeNativeDirect, targetRT.Config.RuntimeMode)
	}
	if targetRT.Config.Model != "claude-opus-4-6" {
		t.Fatalf("expected target model claude-opus-4-6, got %q", targetRT.Config.Model)
	}
	if !targetRT.State.Claude.Applied {
		t.Fatalf("expected target state applied=true after failover: %+v", targetRT.State.Claude)
	}
	if targetRT.State.Claude.AppliedGeneration != active.ActiveGeneration {
		t.Fatalf("expected generation sync between active and target state: active=%s state=%s", active.ActiveGeneration, targetRT.State.Claude.AppliedGeneration)
	}
	settingsBody, err := os.ReadFile(targetRT.Config.SettingsPath)
	if err != nil {
		t.Fatalf("read settings failed: %v", err)
	}
	if !strings.Contains(string(settingsBody), "\"model\": \"claude-opus-4-6\"") {
		t.Fatalf("expected claude model in settings, got:\n%s", string(settingsBody))
	}
	if strings.Contains(string(settingsBody), "http://127.0.0.1:") {
		t.Fatalf("native-direct settings should not target local proxy, got:\n%s", string(settingsBody))
	}
}

func TestFailoverRollsBackOnDoctorFailure(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)

	var revertCalls int32
	reg := provider.NewRegistry()
	if err := reg.Register(providercodex.NewBundle()); err != nil {
		t.Fatalf("register codex provider failed: %v", err)
	}
	if err := reg.Register(provider.Bundle{
		VendorID:     "claude",
		RuntimeModes: []string{string(config.RuntimeModeNativeDirect)},
		Capabilities: map[provider.Capability]bool{
			provider.CapabilityClaude: true,
		},
		Claude: &countingClaudePatcher{revertCalls: &revertCalls},
	}); err != nil {
		t.Fatalf("register claude provider failed: %v", err)
	}

	app := &application{
		home:            tmpHome,
		cwd:             tmpHome,
		username:        "tester",
		registry:        reg,
		backendRegistry: backends.DefaultRegistry(),
	}
	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "default"}); err != nil {
		t.Fatalf("bootstrap source failed: %v", err)
	}

	settingsPath := filepath.Join(tmpHome, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings dir failed: %v", err)
	}
	originalSettings := []byte("{\n  \"env\": {\n    \"CUSTOM\": \"keep\"\n  }\n}\n")
	if err := os.WriteFile(settingsPath, originalSettings, 0o600); err != nil {
		t.Fatalf("write original settings failed: %v", err)
	}

	err := app.cmdFailover([]string{
		"--from", "codex:default",
		"--to", "claude:default",
		"--model", "claude-opus-4-6",
	})
	if err == nil {
		t.Fatal("expected failover failure due doctor validation")
	}
	if code := cberr.Code(err); code != cberr.ErrSwitchValidation && code != cberr.ErrRollbackFailed {
		t.Fatalf("unexpected failover error code: %s (%v)", code, err)
	}

	activePath := scope.BuildPaths(tmpHome, tmpHome, scope.MustRef("codex", "default")).ActivePath
	active, loadErr := control.LoadActive(activePath)
	if loadErr != nil {
		t.Fatalf("load active failed: %v", loadErr)
	}
	if active.ActiveVendor != "codex" || active.ActiveProfile != "default" {
		t.Fatalf("expected active pointer restored to source scope, got: %+v", active)
	}

	targetScopeDir := scope.BuildPaths(tmpHome, tmpHome, scope.MustRef("claude", "default")).ScopeDir
	if _, statErr := os.Stat(targetScopeDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected target scope rollback removal, stat err=%v", statErr)
	}

	restored, readErr := os.ReadFile(settingsPath)
	if readErr != nil {
		t.Fatalf("read restored settings failed: %v", readErr)
	}
	if string(restored) != string(originalSettings) {
		t.Fatalf("expected settings restored after rollback\nwant:\n%s\ngot:\n%s", string(originalSettings), string(restored))
	}
	if atomic.LoadInt32(&revertCalls) == 0 {
		t.Fatal("expected provider-specific revert to be invoked during failover rollback")
	}
}

func TestFailoverRestoresTargetScopeOnBootstrapFailure(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)

	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}

	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "default"}); err != nil {
		t.Fatalf("bootstrap source failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{
		"--vendor", "claude",
		"--profile", "default",
		"--runtime-mode", string(config.RuntimeModeNativeDirect),
		"--model", "claude-sonnet-4-6",
	}); err != nil {
		t.Fatalf("bootstrap target baseline failed: %v", err)
	}

	targetRef := scope.MustRef("claude", "default")
	targetBefore, err := app.loadRuntime(targetRef, false)
	if err != nil {
		t.Fatalf("load target baseline runtime failed: %v", err)
	}
	if targetBefore.Config.Model != "claude-sonnet-4-6" {
		t.Fatalf("expected baseline target model claude-sonnet-4-6, got %q", targetBefore.Config.Model)
	}

	globalPaths := scope.BuildPaths(tmpHome, tmpHome, scope.MustRef("codex", "default"))
	if err := os.MkdirAll(filepath.Dir(globalPaths.SwitchLock), 0o755); err != nil {
		t.Fatalf("prepare lock dir failed: %v", err)
	}
	if _, err := os.Stat(globalPaths.SwitchLock); os.IsNotExist(err) {
		if err := os.WriteFile(globalPaths.SwitchLock, []byte{}, 0o644); err != nil {
			t.Fatalf("prepare lock file failed: %v", err)
		}
	} else if err != nil {
		t.Fatalf("stat lock file failed: %v", err)
	}
	if err := os.Chmod(globalPaths.SwitchLock, 0o444); err != nil {
		t.Fatalf("chmod lock file readonly failed: %v", err)
	}
	defer func() {
		_ = os.Chmod(globalPaths.SwitchLock, 0o644)
	}()

	err = app.cmdFailover([]string{
		"--from", "codex:default",
		"--to", "claude:default",
		"--model", "claude-opus-4-6",
	})
	if err == nil {
		t.Fatal("expected failover failure due bootstrap lock acquisition error")
	}
	if code := cberr.Code(err); code != cberr.ErrSwitchFailed && code != cberr.ErrRollbackFailed {
		t.Fatalf("unexpected failover error code: %s (%v)", code, err)
	}

	targetAfter, err := app.loadRuntime(targetRef, false)
	if err != nil {
		t.Fatalf("load target runtime after failover failure failed: %v", err)
	}
	if targetAfter.Config.Model != "claude-sonnet-4-6" {
		t.Fatalf("expected target config rollback to baseline model, got %q", targetAfter.Config.Model)
	}

	active, err := control.LoadActive(globalPaths.ActivePath)
	if err != nil {
		t.Fatalf("load active pointer failed: %v", err)
	}
	if active.ActiveVendor != "codex" || active.ActiveProfile != "default" {
		t.Fatalf("expected active pointer unchanged after bootstrap failure, got %+v", active)
	}
}

func TestFailoverRejectsMismatchedExistingTargetBeforeMutation(t *testing.T) {
	tmpHome := t.TempDir()
	cwdA := filepath.Join(tmpHome, "project-a")
	cwdB := filepath.Join(tmpHome, "project-b")
	if err := os.MkdirAll(cwdA, 0o755); err != nil {
		t.Fatalf("mkdir project-a failed: %v", err)
	}
	if err := os.MkdirAll(cwdB, 0o755); err != nil {
		t.Fatalf("mkdir project-b failed: %v", err)
	}

	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", cwdA)
	appA, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication project-a failed: %v", err)
	}
	if err := appA.cmdBootstrap([]string{
		"--vendor", "claude",
		"--profile", "target",
		"--runtime-mode", string(config.RuntimeModeNativeDirect),
		"--model", "claude-sonnet-4-6",
	}); err != nil {
		t.Fatalf("bootstrap target on project-a failed: %v", err)
	}

	targetRef := scope.MustRef("claude", "target")
	targetBefore, err := appA.loadRuntime(targetRef, false)
	if err != nil {
		t.Fatalf("load target before failover failed: %v", err)
	}
	if targetBefore.Config.Model != "claude-sonnet-4-6" {
		t.Fatalf("unexpected baseline target model: %s", targetBefore.Config.Model)
	}

	t.Setenv("CCB_CWD", cwdB)
	appB, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication project-b failed: %v", err)
	}
	if err := appB.cmdBootstrap([]string{
		"--vendor", "claude",
		"--profile", "source",
		"--runtime-mode", string(config.RuntimeModeNativeDirect),
		"--model", "claude-opus-4-6",
	}); err != nil {
		t.Fatalf("bootstrap source on project-b failed: %v", err)
	}
	if err := appB.cmdUse([]string{"--vendor", "claude", "--profile", "source"}); err != nil {
		t.Fatalf("use source scope failed: %v", err)
	}

	err = appB.cmdFailover([]string{
		"--from", "claude:source",
		"--to", "claude:target",
		"--model", "claude-opus-4-6",
	})
	if err == nil {
		t.Fatal("expected failover failure for mismatched existing target settings binding")
	}
	if code := cberr.Code(err); code != cberr.ErrConfigOverridden {
		t.Fatalf("unexpected failover error code: %s (%v)", code, err)
	}

	targetAfter, err := appB.loadRuntime(targetRef, false)
	if err != nil {
		t.Fatalf("load target after failover failed: %v", err)
	}
	if targetAfter.Config.Model != "claude-sonnet-4-6" {
		t.Fatalf("target model was mutated despite mismatch failure: got=%s", targetAfter.Config.Model)
	}
}

func TestFailoverRollbackCleansNewGatewayTargetArtifacts(t *testing.T) {
	tmpHome := t.TempDir()
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nset -euo pipefail\ncmd=\"${1:-}\"\nif [[ \"$cmd\" == \"kickstart\" ]]; then\n  target=\"${3:-}\"\n  if [[ \"$target\" == *\".ccb.codex.target.\"* ]]; then\n    printf 'simulated target kickstart failure\\n' >&2\n    exit 91\n  fi\nfi\nif [[ \"$cmd\" == \"print\" ]]; then\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}

	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)

	authSource := filepath.Join(tmpHome, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authSource), 0o755); err != nil {
		t.Fatalf("mkdir auth source dir failed: %v", err)
	}
	if err := os.WriteFile(authSource, []byte("{\"token\":\"ok\"}\n"), 0o600); err != nil {
		t.Fatalf("write auth source failed: %v", err)
	}

	backendReg := backend.NewRegistry()
	if err := backendReg.Register(backend.Bundle{
		ID: config.DefaultGatewayBackend,
		Capabilities: map[backend.Capability]bool{
			backend.CapabilityArtifact: true,
			backend.CapabilityProxy:    true,
			backend.CapabilityHealth:   true,
		},
		Artifact: fakeBackendArtifactInstaller{},
		Proxy:    noopBackendProxyRenderer{},
		Health:   noopBackendHealthChecker{},
	}); err != nil {
		t.Fatalf("register backend failed: %v", err)
	}

	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	app.backendRegistry = backendReg

	if err := app.cmdBootstrap([]string{
		"--vendor", "claude",
		"--profile", "source",
		"--runtime-mode", string(config.RuntimeModeNativeDirect),
		"--model", "claude-opus-4-6",
	}); err != nil {
		t.Fatalf("bootstrap source failed: %v", err)
	}
	if err := app.cmdUse([]string{"--vendor", "claude", "--profile", "source"}); err != nil {
		t.Fatalf("use source scope failed: %v", err)
	}

	err = app.cmdFailover([]string{
		"--from", "claude:source",
		"--to", "codex:target",
		"--model", "gpt-5.3-codex",
	})
	if err == nil {
		t.Fatal("expected failover failure from target gateway start error")
	}
	if code := cberr.Code(err); code != cberr.ErrSwitchFailed && code != cberr.ErrRollbackFailed {
		t.Fatalf("unexpected failover error code: %s (%v)", code, err)
	}

	targetRef := scope.MustRef("codex", "target")
	targetPaths := scope.BuildPaths(app.home, app.cwd, targetRef)
	if _, statErr := os.Stat(targetPaths.ScopeDir); !os.IsNotExist(statErr) {
		t.Fatalf("expected target scope removal after rollback, stat err=%v", statErr)
	}

	proxyLabel, syncLabel := targetRef.Labels(app.username)
	proxyPlist := filepath.Join(targetPaths.LaunchAgentDir, proxyLabel+".plist")
	syncPlist := filepath.Join(targetPaths.LaunchAgentDir, syncLabel+".plist")
	if _, statErr := os.Stat(proxyPlist); !os.IsNotExist(statErr) {
		t.Fatalf("expected proxy plist cleanup after rollback, stat err=%v", statErr)
	}
	if _, statErr := os.Stat(syncPlist); !os.IsNotExist(statErr) {
		t.Fatalf("expected sync plist cleanup after rollback, stat err=%v", statErr)
	}
}

func TestFailoverRoundtripCodexClaudeCodex(t *testing.T) {
	tmpHome := t.TempDir()
	stub := filepath.Join(tmpHome, "launchctl")
	stubScript := "#!/usr/bin/env bash\nset -euo pipefail\nif [[ \"${1:-}\" == \"print\" ]]; then\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)

	authSource := filepath.Join(tmpHome, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(authSource), 0o755); err != nil {
		t.Fatalf("mkdir auth source dir failed: %v", err)
	}
	if err := os.WriteFile(authSource, []byte("{\"token\":\"ok\"}\n"), 0o600); err != nil {
		t.Fatalf("write auth source failed: %v", err)
	}

	backendReg := backend.NewRegistry()
	if err := backendReg.Register(backend.Bundle{
		ID: "stub-backend",
		Capabilities: map[backend.Capability]bool{
			backend.CapabilityArtifact: true,
			backend.CapabilityProxy:    true,
			backend.CapabilityHealth:   true,
		},
		Artifact: fakeBackendArtifactInstaller{},
		Proxy:    noopBackendProxyRenderer{},
		Health:   noopBackendHealthChecker{},
	}); err != nil {
		t.Fatalf("register backend failed: %v", err)
	}

	reg := provider.NewRegistry()
	codexBundle := providercodex.NewBundle()
	codexBundle.Auth = noopAuthStrategy{}
	if err := reg.Register(codexBundle); err != nil {
		t.Fatalf("register codex provider failed: %v", err)
	}
	if err := reg.Register(providerclaude.NewBundle()); err != nil {
		t.Fatalf("register claude provider failed: %v", err)
	}

	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	app.registry = reg
	app.backendRegistry = backendReg

	if err := app.cmdSetup([]string{
		"--vendor", "codex",
		"--profile", "default",
		"--runtime-mode", "gateway",
		"--gateway-backend", "stub-backend",
	}); err != nil {
		t.Fatalf("codex setup failed: %v", err)
	}
	if err := app.cmdFailover([]string{
		"--from", "codex:default",
		"--to", "claude:default",
		"--model", "claude-opus-4-6",
	}); err != nil {
		t.Fatalf("codex->claude failover failed: %v", err)
	}
	if err := app.cmdFailover([]string{
		"--from", "claude:default",
		"--to", "codex:default",
		"--model", "gpt-5.3-codex",
	}); err != nil {
		t.Fatalf("claude->codex failover failed: %v", err)
	}

	paths := scope.BuildPaths(tmpHome, tmpHome, scope.MustRef("codex", "default"))
	active, err := control.LoadActive(paths.ActivePath)
	if err != nil {
		t.Fatalf("load active failed: %v", err)
	}
	if active.ActiveVendor != "codex" || active.ActiveProfile != "default" {
		t.Fatalf("unexpected active scope after roundtrip failover: %+v", active)
	}
	if strings.TrimSpace(active.ActiveGeneration) == "" {
		t.Fatalf("expected non-empty active generation after roundtrip failover: %+v", active)
	}
}

func TestPreflightCodexToClaudeReturnsNoBlockingFailures(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)

	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "default"}); err != nil {
		t.Fatalf("bootstrap codex failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", "claude", "--profile", "default", "--runtime-mode", "native-direct"}); err != nil {
		t.Fatalf("bootstrap claude failed: %v", err)
	}

	err = app.cmdPreflight([]string{
		"--from", "codex:default",
		"--to", "claude:default",
		"--model", "claude-opus-4-6",
	})
	if err != nil {
		t.Fatalf("preflight should pass with advisory warnings only: %v", err)
	}
}

func TestPreflightBlocksInvalidTargetModelPolicy(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)

	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "default"}); err != nil {
		t.Fatalf("bootstrap source failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "backup"}); err != nil {
		t.Fatalf("bootstrap target failed: %v", err)
	}

	err = app.cmdPreflight([]string{
		"--from", "codex:default",
		"--to", "codex:backup",
		"--model", "claude-opus-4-6",
	})
	if err == nil {
		t.Fatal("expected preflight failure for invalid codex target model")
	}
	if code := cberr.Code(err); code != cberr.ErrSwitchValidation {
		t.Fatalf("unexpected preflight error code: %s (%v)", code, err)
	}
}

func TestHandoffCreateWritesMarkdownBundle(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("CCB_HOME", tmpHome)
	t.Setenv("CCB_CWD", tmpHome)

	app, err := newApplication()
	if err != nil {
		t.Fatalf("newApplication failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", "codex", "--profile", "default"}); err != nil {
		t.Fatalf("bootstrap codex failed: %v", err)
	}
	if err := app.cmdBootstrap([]string{"--vendor", "claude", "--profile", "default", "--runtime-mode", "native-direct"}); err != nil {
		t.Fatalf("bootstrap claude failed: %v", err)
	}

	outPath := filepath.Join(tmpHome, "handoff.md")
	if err := app.cmdHandoff([]string{
		"create",
		"--from", "codex:default",
		"--to", "claude:default",
		"--model", "claude-opus-4-6",
		"--output", outPath,
	}); err != nil {
		t.Fatalf("handoff create failed: %v", err)
	}

	body, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read handoff bundle failed: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "# ccgateway failover handoff") {
		t.Fatalf("unexpected handoff heading: %s", text)
	}
	if !strings.Contains(text, "ccb failover --from codex:default --to claude:default --model claude-opus-4-6") {
		t.Fatalf("expected failover command in handoff bundle, got: %s", text)
	}
}

type noopBackendProxyRenderer struct{}

func (noopBackendProxyRenderer) WriteProxyConfig(_ context.Context, rt backend.Runtime) error {
	if err := os.MkdirAll(filepath.Dir(rt.Paths.ProxyConfig), 0o755); err != nil {
		return err
	}
	body := "model: " + rt.Config.Model + "\n"
	return os.WriteFile(rt.Paths.ProxyConfig, []byte(body), 0o644)
}

func (noopBackendProxyRenderer) WriteSyncScript(_ context.Context, _ backend.Runtime, _ string) error {
	return nil
}

type noopBackendHealthChecker struct{}

func (noopBackendHealthChecker) Check(_ context.Context, _ backend.Runtime) error {
	return nil
}

type fakeBackendArtifactInstaller struct{}

func (fakeBackendArtifactInstaller) Install(_ context.Context, rt backend.Runtime, _ string) (backend.ArtifactResult, error) {
	if err := os.MkdirAll(filepath.Dir(rt.Paths.ProxyBinary), 0o755); err != nil {
		return backend.ArtifactResult{}, err
	}
	if err := os.WriteFile(rt.Paths.ProxyBinary, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
		return backend.ArtifactResult{}, err
	}
	return backend.ArtifactResult{
		Version:    "v-test",
		SHA256:     "sha256-test",
		BinaryPath: rt.Paths.ProxyBinary,
	}, nil
}

type flakyBackendHealthChecker struct {
	mu                sync.Mutex
	failuresRemaining int
	calls             int
}

func (h *flakyBackendHealthChecker) Check(_ context.Context, _ backend.Runtime) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls++
	if h.failuresRemaining > 0 {
		h.failuresRemaining--
		return context.DeadlineExceeded
	}
	return nil
}

func (h *flakyBackendHealthChecker) Calls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.calls
}

type noopClaudePatcher struct{}

func (noopClaudePatcher) Apply(_ context.Context, _ provider.ScopeRuntime, _ string) (claudepkg.ApplyResult, error) {
	return claudepkg.ApplyResult{SnapshotPath: "noop-snapshot", SnapshotSHA256: "noop-sha"}, nil
}

func (noopClaudePatcher) Revert(_ context.Context, _ provider.ScopeRuntime, _, _ string) error {
	return nil
}

type countingClaudePatcher struct {
	revertCalls *int32
}

func (p *countingClaudePatcher) Apply(_ context.Context, _ provider.ScopeRuntime, _ string) (claudepkg.ApplyResult, error) {
	return claudepkg.ApplyResult{SnapshotPath: "noop-snapshot", SnapshotSHA256: "noop-sha"}, nil
}

func (p *countingClaudePatcher) Revert(_ context.Context, _ provider.ScopeRuntime, _, _ string) error {
	if p != nil && p.revertCalls != nil {
		atomic.AddInt32(p.revertCalls, 1)
	}
	return nil
}

type noopAuthStrategy struct{}

func (noopAuthStrategy) Sync(_ context.Context, _ provider.ScopeRuntime) error {
	return nil
}
