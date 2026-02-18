package launchd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallAgentsCleansUpOnProxyBootstrapFailure(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "launchctl.log")
	stubPath := filepath.Join(tmp, "launchctl")
	proxyPlistPath := filepath.Join(tmp, "launchd", "proxy.plist")
	syncPlistPath := filepath.Join(tmp, "launchd", "sync.plist")

	stubScript := "#!/usr/bin/env bash\nset -euo pipefail\nprintf '%s\\n' \"$*\" >> \"${CCB_TEST_LAUNCHCTL_LOG}\"\nif [[ \"${1:-}\" == \"bootstrap\" && \"${3:-}\" == \"${CCB_TEST_FAIL_PROXY_PLIST}\" ]]; then\n  printf 'simulated proxy bootstrap failure\\n' >&2\n  exit 17\nfi\nexit 0\n"
	if err := os.WriteFile(stubPath, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write launchctl stub failed: %v", err)
	}
	t.Setenv("CCB_TEST_LAUNCHCTL_LOG", logPath)
	t.Setenv("CCB_TEST_FAIL_PROXY_PLIST", proxyPlistPath)

	mgr := &Manager{UID: os.Getuid(), LaunchctlBin: stubPath}
	files := AgentFiles{
		ProxyUnitPath: proxyPlistPath,
		SyncUnitPath:  syncPlistPath,
		ProxyBinary:   filepath.Join(tmp, "proxy", "cli-proxy-api"),
		ProxyConfig:   filepath.Join(tmp, "proxy", "config.yaml"),
		ProxyLog:      filepath.Join(tmp, "logs", "proxy.log"),
		SyncLog:       filepath.Join(tmp, "logs", "sync.log"),
		SyncScript:    filepath.Join(tmp, "launchd", "sync.sh"),
		AuthSource:    filepath.Join(tmp, "auth.json"),
		HomeDir:       tmp,
		ProxyLabel:    "com.test.proxy",
		SyncLabel:     "com.test.sync",
	}

	err := mgr.InstallAgents(files)
	if err == nil {
		t.Fatal("expected install failure")
	}
	if !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("expected bootstrap error context, got: %v", err)
	}

	if _, statErr := os.Stat(proxyPlistPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected proxy plist cleanup, stat error=%v", statErr)
	}
	if _, statErr := os.Stat(syncPlistPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected sync plist cleanup, stat error=%v", statErr)
	}

	logBody, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read launchctl log failed: %v", readErr)
	}
	logText := string(logBody)
	proxyBootout := fmt.Sprintf("bootout gui/%d/%s", os.Getuid(), files.ProxyLabel)
	syncBootout := fmt.Sprintf("bootout gui/%d/%s", os.Getuid(), files.SyncLabel)
	if strings.Count(logText, proxyBootout) < 2 {
		t.Fatalf("expected cleanup bootout retry for proxy label, logs:\n%s", logText)
	}
	if strings.Count(logText, syncBootout) < 2 {
		t.Fatalf("expected cleanup bootout retry for sync label, logs:\n%s", logText)
	}
}
