package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ccgateway/internal/proxy"
	"ccgateway/internal/service/systemd"
)

func TestSystemdManagerWithStub(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "systemctl.log")
	stub := filepath.Join(dir, "systemctl")
	// Stub script that logs all calls and simulates basic systemctl behaviour.
	script := `#!/usr/bin/env bash
set -euo pipefail
echo "$@" >> "` + logFile + `"
# Simulate "show --property=LoadState" returning "loaded" after install.
if [[ "${1:-}" == "--user" && "${2:-}" == "show" ]]; then
  echo "loaded"
  exit 0
fi
exit 0
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write stub: %v", err)
	}
	t.Setenv("CCG_SYSTEMCTL_BIN", stub)

	mgr := systemd.NewManager()
	unitDir := filepath.Join(dir, ".config", "systemd", "user")
	files := systemd.UnitFiles{
		ProxyUnitPath: filepath.Join(unitDir, "com.test.proxy.service"),
		SyncUnitPath:  filepath.Join(unitDir, "com.test.sync.service"),
		ProxyBinary:   "/tmp/cli-proxy-api",
		ProxyConfig:   filepath.Join(dir, "proxy.yaml"),
		ProxyLog:      filepath.Join(dir, "proxy.log"),
		SyncLog:       filepath.Join(dir, "sync.log"),
		SyncScript:    filepath.Join(dir, "sync.sh"),
		HomeDir:       dir,
		ProxyLabel:    "com.test.proxy",
		SyncLabel:     "com.test.sync",
	}
	if err := proxy.WriteSyncScript(files.SyncScript, "/usr/local/bin/ccg", "codex", "default"); err != nil {
		t.Fatalf("write sync script: %v", err)
	}
	if err := proxy.WriteProxyConfig(files.ProxyConfig, 12345, filepath.Join(dir, "auths"), "gpt-5.3-codex"); err != nil {
		t.Fatalf("write proxy config: %v", err)
	}

	if err := mgr.InstallUnits(files); err != nil {
		t.Fatalf("install units failed: %v", err)
	}

	// Verify unit files were written.
	if _, err := os.Stat(files.ProxyUnitPath); err != nil {
		t.Fatalf("proxy unit file missing after install: %v", err)
	}
	if _, err := os.Stat(files.SyncUnitPath); err != nil {
		t.Fatalf("sync unit file missing after install: %v", err)
	}

	// Verify proxy unit content.
	proxyContent, err := os.ReadFile(files.ProxyUnitPath)
	if err != nil {
		t.Fatalf("read proxy unit: %v", err)
	}
	if !strings.Contains(string(proxyContent), "ExecStart=/tmp/cli-proxy-api") {
		t.Fatalf("proxy unit missing ExecStart, got:\n%s", proxyContent)
	}
	if !strings.Contains(string(proxyContent), "Restart=always") {
		t.Fatalf("proxy unit missing Restart=always, got:\n%s", proxyContent)
	}

	// Verify sync unit content.
	syncContent, err := os.ReadFile(files.SyncUnitPath)
	if err != nil {
		t.Fatalf("read sync unit: %v", err)
	}
	if !strings.Contains(string(syncContent), "Type=oneshot") {
		t.Fatalf("sync unit missing Type=oneshot, got:\n%s", syncContent)
	}

	if err := mgr.Start(files.ProxyLabel, files.SyncLabel); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	status, err := mgr.Status(files.ProxyLabel, files.SyncLabel)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if !status.ProxyLoaded || !status.SyncLoaded {
		t.Fatalf("expected loaded statuses true: %#v", status)
	}
	if err := mgr.Stop(files.ProxyLabel, files.SyncLabel); err != nil {
		t.Fatalf("stop failed: %v", err)
	}
	if err := mgr.RemoveUnits(files.ProxyLabel, files.SyncLabel, files.ProxyUnitPath, files.SyncUnitPath); err != nil {
		t.Fatalf("remove failed: %v", err)
	}

	b, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log failed: %v", err)
	}
	logText := string(b)
	for _, keyword := range []string{"daemon-reload", "enable", "start", "show", "stop", "disable"} {
		if !strings.Contains(logText, keyword) {
			t.Fatalf("expected %q call in systemctl log:\n%s", keyword, logText)
		}
	}
}
