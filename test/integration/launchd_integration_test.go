package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ccgateway/internal/launchd"
)

func TestLaunchdManagerWithStub(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "launchctl.log")
	stub := filepath.Join(dir, "launchctl")
	script := "#!/usr/bin/env bash\nset -euo pipefail\necho \"$@\" >> \"$CCB_TEST_LAUNCHCTL_LOG\"\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write stub: %v", err)
	}
	t.Setenv("CCB_TEST_LAUNCHCTL_LOG", logFile)
	t.Setenv("CCB_LAUNCHCTL_BIN", stub)

	mgr := launchd.NewManager()
	files := launchd.AgentFiles{
		ProxyPlistPath: filepath.Join(dir, "proxy.plist"),
		SyncPlistPath:  filepath.Join(dir, "sync.plist"),
		ProxyBinary:    "/tmp/cli-proxy-api",
		ProxyConfig:    filepath.Join(dir, "proxy.yaml"),
		ProxyLog:       filepath.Join(dir, "proxy.log"),
		SyncLog:        filepath.Join(dir, "sync.log"),
		SyncScript:     filepath.Join(dir, "sync.sh"),
		AuthSource:     filepath.Join(dir, "auth.json"),
		HomeDir:        dir,
		ProxyLabel:     "com.test.proxy",
		SyncLabel:      "com.test.sync",
	}
	if err := os.WriteFile(files.AuthSource, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write auth source: %v", err)
	}
	if err := launchd.WriteSyncScript(files.SyncScript, "/usr/local/bin/ccb", "codex", "default"); err != nil {
		t.Fatalf("write sync script: %v", err)
	}
	if err := launchd.WriteProxyConfig(files.ProxyConfig, 12345, filepath.Join(dir, "auths"), "gpt-5.3-codex"); err != nil {
		t.Fatalf("write proxy config: %v", err)
	}

	if err := mgr.InstallAgents(files); err != nil {
		t.Fatalf("install agents failed: %v", err)
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
	if err := mgr.RemoveAgents(files.ProxyLabel, files.SyncLabel, files.ProxyPlistPath, files.SyncPlistPath); err != nil {
		t.Fatalf("remove failed: %v", err)
	}

	b, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("read log failed: %v", err)
	}
	logText := string(b)
	for _, keyword := range []string{"bootstrap", "kickstart", "print", "bootout"} {
		if !strings.Contains(logText, keyword) {
			t.Fatalf("expected %q call in launchctl log: %s", keyword, logText)
		}
	}
}
