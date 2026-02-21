//go:build linux

package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ccgateway/internal/proxy"
	"ccgateway/internal/service"
)

func TestSystemdManagerWithStub(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "systemctl.log")
	stub := filepath.Join(dir, "systemctl")
	script := "#!/usr/bin/env bash\nset -euo pipefail\necho \"$@\" >> \"$CCG_TEST_SYSTEMCTL_LOG\"\nif [[ \"${2:-}\" == \"show\" ]]; then\n  echo \"loaded\"\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write stub: %v", err)
	}
	t.Setenv("CCG_TEST_SYSTEMCTL_LOG", logFile)
	t.Setenv("CCG_SYSTEMCTL_BIN", stub)

	mgr := service.NewManager()
	files := service.ServiceFiles{
		ProxyUnitPath: filepath.Join(dir, ".config", "systemd", "user", "ccgateway-test-proxy.service"),
		SyncUnitPath:  filepath.Join(dir, ".config", "systemd", "user", "ccgateway-test-sync.service"),
		ProxyBinary:   "/tmp/cli-proxy-api",
		ProxyConfig:   filepath.Join(dir, "proxy.yaml"),
		ProxyLog:      filepath.Join(dir, "proxy.log"),
		SyncLog:       filepath.Join(dir, "sync.log"),
		SyncScript:    filepath.Join(dir, "sync.sh"),
		AuthSource:    filepath.Join(dir, "auth.json"),
		HomeDir:       dir,
		ProxyLabel:    "ccgateway-test-proxy",
		SyncLabel:     "ccgateway-test-sync",
	}
	if err := os.WriteFile(files.AuthSource, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write auth source: %v", err)
	}
	if err := proxy.WriteSyncScript(files.SyncScript, "/usr/local/bin/ccg", "codex", "default"); err != nil {
		t.Fatalf("write sync script: %v", err)
	}
	if err := proxy.WriteProxyConfig(files.ProxyConfig, 12345, filepath.Join(dir, "auths"), "gpt-5.3-codex"); err != nil {
		t.Fatalf("write proxy config: %v", err)
	}

	if err := mgr.Install(files); err != nil {
		t.Fatalf("install failed: %v", err)
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
	if err := mgr.Remove(files.ProxyLabel, files.SyncLabel, files.ProxyUnitPath, files.SyncUnitPath); err != nil {
		t.Fatalf("remove failed: %v", err)
	}

	b, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log failed: %v", err)
	}
	logText := string(b)
	for _, keyword := range []string{"daemon-reload", "enable", "start", "show", "stop", "disable"} {
		if !strings.Contains(logText, keyword) {
			t.Fatalf("expected %q call in systemctl log: %s", keyword, logText)
		}
	}
}
