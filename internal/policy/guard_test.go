package policy

import (
	"os"
	"path/filepath"
	"testing"

	"ccgateway/internal/config"
	"ccgateway/internal/scope"
)

func TestNormalizeMode(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{in: "", want: ModeStrict, ok: true},
		{in: "strict", want: ModeStrict, ok: true},
		{in: "compat", want: ModeCompat, ok: true},
		{in: "bad", ok: false},
	}
	for _, tc := range tests {
		got, ok := NormalizeMode(tc.in)
		if ok != tc.ok {
			t.Fatalf("NormalizeMode(%q) ok=%t, want %t", tc.in, ok, tc.ok)
		}
		if ok && got != tc.want {
			t.Fatalf("NormalizeMode(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestEvaluateCodexStrictRejectsUserLayer(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, cwd, ref)
	cfg := config.DefaultForScope(home, cwd, ref.VendorID, ref.ProfileID)
	cfg.PolicyMode = ModeStrict
	cfg.SettingsLayer = config.SettingsLayerUser
	cfg.SettingsPath = paths.ClaudeUserSettingsPath

	ev := Evaluate(paths, cfg)
	if ev.Mode != ModeStrict {
		t.Fatalf("expected strict mode, got %q", ev.Mode)
	}
	if len(ev.Violations) == 0 {
		t.Fatal("expected policy violation for settings_layer=user")
	}
}

func TestEvaluateCodexStrictRejectsLooseAuthPermissions(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, cwd, ref)
	cfg := config.DefaultForScope(home, cwd, ref.VendorID, ref.ProfileID)
	cfg.PolicyMode = ModeStrict
	cfg.RuntimeMode = config.RuntimeModeGateway
	cfg.ProxyEnabled = true
	cfg.AuthSource = filepath.Join(home, ".codex", "auth.json")
	if err := os.MkdirAll(filepath.Dir(cfg.AuthSource), 0o755); err != nil {
		t.Fatalf("mkdir auth dir failed: %v", err)
	}
	if err := os.WriteFile(cfg.AuthSource, []byte("{\"token\":\"x\"}\n"), 0o644); err != nil {
		t.Fatalf("write auth source failed: %v", err)
	}

	ev := Evaluate(paths, cfg)
	if len(ev.Violations) == 0 {
		t.Fatal("expected permission violation")
	}
}

func TestEvaluateCodexForcesStrictEvenWhenCompat(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd failed: %v", err)
	}
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, cwd, ref)
	cfg := config.DefaultForScope(home, cwd, ref.VendorID, ref.ProfileID)
	cfg.PolicyMode = ModeCompat
	cfg.SettingsLayer = config.SettingsLayerUser

	ev := Evaluate(paths, cfg)
	if ev.Mode != ModeStrict {
		t.Fatalf("expected strict mode for codex, got %q", ev.Mode)
	}
	if len(ev.Violations) == 0 {
		t.Fatal("expected codex strict violation for settings_layer=user")
	}
}
