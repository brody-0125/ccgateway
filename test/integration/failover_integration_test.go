package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ccgateway/internal/cli"
	"ccgateway/internal/control"
	"ccgateway/internal/scope"
)

func TestCodexToClaudeFailoverIntegration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CCB_HOME", home)
	t.Setenv("CCB_CWD", home)

	run := func(args ...string) {
		t.Helper()
		if code := cli.Run(args); code != 0 {
			t.Fatalf("cli.Run(%v) returned non-zero exit code: %d", args, code)
		}
	}

	// Source scope exists and is active, but we intentionally do not start codex service
	// to simulate a fallback scenario (token/quota/service issues).
	run("bootstrap", "--vendor", "codex", "--profile", "default")
	run("failover", "--from", "codex:default", "--to", "claude:default", "--model", "claude-opus-4-6")
	run("doctor", "--vendor", "claude", "--profile", "default")

	activePath := scope.BuildPaths(home, home, scope.MustRef("codex", "default")).ActivePath
	active, err := control.LoadActive(activePath)
	if err != nil {
		t.Fatalf("load active pointer failed: %v", err)
	}
	if active.ActiveVendor != "claude" || active.ActiveProfile != "default" {
		t.Fatalf("unexpected active scope after failover: %+v", active)
	}
	if strings.TrimSpace(active.ActiveGeneration) == "" {
		t.Fatalf("expected non-empty active generation after failover: %+v", active)
	}

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	body, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings failed: %v", err)
	}
	if !strings.Contains(string(body), "\"model\": \"claude-opus-4-6\"") {
		t.Fatalf("expected failover target model in settings, got:\n%s", string(body))
	}
	if strings.Contains(string(body), "http://127.0.0.1:") {
		t.Fatalf("native-direct settings must not point to local proxy, got:\n%s", string(body))
	}
}
