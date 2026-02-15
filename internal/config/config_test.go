package config

import "testing"

func TestDefaultForScopeUsesLatestProxyVersion(t *testing.T) {
	cfg := DefaultForScope("/tmp/home", "/tmp/cwd", "codex", "default")
	if cfg.ProxyVersion != "latest" {
		t.Fatalf("expected proxy_version=latest, got %q", cfg.ProxyVersion)
	}
	if cfg.GatewayBackend != DefaultGatewayBackend {
		t.Fatalf("expected gateway_backend=%q, got %q", DefaultGatewayBackend, cfg.GatewayBackend)
	}
	if cfg.PolicyMode != "strict" {
		t.Fatalf("expected policy_mode=strict, got %q", cfg.PolicyMode)
	}
}

func TestDefaultForScopeUsesProjectSettingsByDefault(t *testing.T) {
	cfg := DefaultForScope("/tmp/home", "/tmp/cwd", "codex", "default")
	if cfg.SettingsLayer != SettingsLayerProject {
		t.Fatalf("expected settings_layer=%q, got %q", SettingsLayerProject, cfg.SettingsLayer)
	}
	if cfg.SettingsPath != "/tmp/cwd/.claude/settings.json" {
		t.Fatalf("expected project settings path, got %q", cfg.SettingsPath)
	}
}

func TestDefaultForScopeClaudeUsesNativeDirect(t *testing.T) {
	cfg := DefaultForScope("/tmp/home", "/tmp/cwd", "claude", "default")
	if cfg.RuntimeMode != RuntimeModeNativeDirect {
		t.Fatalf("expected runtime_mode=%q, got %q", RuntimeModeNativeDirect, cfg.RuntimeMode)
	}
	if cfg.ProxyEnabled {
		t.Fatal("expected proxy_enabled=false for claude default scope")
	}
	if cfg.Model != "claude-opus-4-6" {
		t.Fatalf("expected default claude model, got %q", cfg.Model)
	}
	if cfg.PolicyMode != "compat" {
		t.Fatalf("expected default claude policy_mode compat, got %q", cfg.PolicyMode)
	}
}

func TestParseRuntimeMode(t *testing.T) {
	tests := []struct {
		in   string
		want RuntimeMode
		ok   bool
	}{
		{in: "gateway", want: RuntimeModeGateway, ok: true},
		{in: "native", want: RuntimeModeNativeCleanup, ok: true},
		{in: "native-cleanup", want: RuntimeModeNativeCleanup, ok: true},
		{in: "native-direct", want: RuntimeModeNativeDirect, ok: true},
		{in: "bad", ok: false},
	}
	for _, tc := range tests {
		got, ok := ParseRuntimeMode(tc.in)
		if ok != tc.ok {
			t.Fatalf("ParseRuntimeMode(%q) ok=%t, want %t", tc.in, ok, tc.ok)
		}
		if ok && got != tc.want {
			t.Fatalf("ParseRuntimeMode(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}
