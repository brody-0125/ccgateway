package systemd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateProxyUnitContent(t *testing.T) {
	files := UnitFiles{
		ProxyLabel:  "com.test.proxy",
		ProxyBinary: "/usr/local/bin/cli-proxy-api",
		ProxyConfig: "/home/user/.ccgateway/config.yaml",
		HomeDir:     "/home/user",
		ProxyLog:    "/home/user/.ccgateway/logs/proxy.log",
	}

	unit := GenerateProxyUnit(files)

	expectations := []string{
		"[Unit]",
		"Description=ccgateway proxy (com.test.proxy)",
		"After=network.target",
		"[Service]",
		"Type=simple",
		fmt.Sprintf("ExecStart=%s --config %s", files.ProxyBinary, files.ProxyConfig),
		fmt.Sprintf("WorkingDirectory=%s", files.HomeDir),
		"Restart=always",
		fmt.Sprintf("StandardOutput=append:%s", files.ProxyLog),
		fmt.Sprintf("StandardError=append:%s", files.ProxyLog),
		"[Install]",
		"WantedBy=default.target",
	}

	for _, exp := range expectations {
		if !strings.Contains(unit, exp) {
			t.Errorf("expected proxy unit to contain %q, got:\n%s", exp, unit)
		}
	}
}

func TestGenerateSyncUnitContent(t *testing.T) {
	files := UnitFiles{
		SyncLabel:  "com.test.sync",
		SyncScript: "/home/user/.ccgateway/sync.sh",
		SyncLog:    "/home/user/.ccgateway/logs/sync.log",
	}

	unit := GenerateSyncUnit(files)

	expectations := []string{
		"[Unit]",
		"Description=ccgateway token-sync (com.test.sync)",
		"After=network.target",
		"[Service]",
		"Type=oneshot",
		fmt.Sprintf("ExecStart=/bin/bash -lc %s", files.SyncScript),
		fmt.Sprintf("StandardOutput=append:%s", files.SyncLog),
		fmt.Sprintf("StandardError=append:%s", files.SyncLog),
		"[Install]",
		"WantedBy=default.target",
	}

	for _, exp := range expectations {
		if !strings.Contains(unit, exp) {
			t.Errorf("expected sync unit to contain %q, got:\n%s", exp, unit)
		}
	}
}

func TestUserDir(t *testing.T) {
	dir := UserDir("/home/testuser")
	expected := filepath.Join("/home/testuser", ".config", "systemd", "user")
	if dir != expected {
		t.Errorf("expected %q, got %q", expected, dir)
	}
}

func TestInstallUnitsCleansUpOnEnableProxyFailure(t *testing.T) {
	tmp := t.TempDir()
	logPath := filepath.Join(tmp, "systemctl.log")
	stubPath := filepath.Join(tmp, "systemctl")
	proxyUnitPath := filepath.Join(tmp, "systemd", "user", "com.test.proxy.service")
	syncUnitPath := filepath.Join(tmp, "systemd", "user", "com.test.sync.service")

	// Stub that fails on "enable com.test.proxy" but succeeds otherwise.
	stubScript := `#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "` + logPath + `"
if [[ "$*" == *"enable"*"com.test.proxy"* ]]; then
  printf 'simulated enable proxy failure\n' >&2
  exit 1
fi
exit 0
`
	if err := os.WriteFile(stubPath, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("write systemctl stub failed: %v", err)
	}

	mgr := &Manager{SystemctlBin: stubPath}
	files := UnitFiles{
		ProxyUnitPath: proxyUnitPath,
		SyncUnitPath:  syncUnitPath,
		ProxyBinary:   filepath.Join(tmp, "proxy", "cli-proxy-api"),
		ProxyConfig:   filepath.Join(tmp, "proxy", "config.yaml"),
		ProxyLog:      filepath.Join(tmp, "logs", "proxy.log"),
		SyncLog:       filepath.Join(tmp, "logs", "sync.log"),
		SyncScript:    filepath.Join(tmp, "sync.sh"),
		HomeDir:       tmp,
		ProxyLabel:    "com.test.proxy",
		SyncLabel:     "com.test.sync",
	}

	err := mgr.InstallUnits(files)
	if err == nil {
		t.Fatal("expected install failure when enable proxy fails")
	}
	if !strings.Contains(err.Error(), "enable proxy") {
		t.Fatalf("expected enable proxy error context, got: %v", err)
	}

	// Verify unit files were cleaned up.
	if _, statErr := os.Stat(proxyUnitPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected proxy unit cleanup, stat error=%v", statErr)
	}
	if _, statErr := os.Stat(syncUnitPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected sync unit cleanup, stat error=%v", statErr)
	}

	logBody, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read systemctl log failed: %v", readErr)
	}
	logText := string(logBody)

	// Should have called disable on the sync label as part of cleanup.
	if !strings.Contains(logText, "disable") {
		t.Fatalf("expected cleanup disable call in systemctl log:\n%s", logText)
	}
}

func TestStatusWithStubSystemctl(t *testing.T) {
	tmp := t.TempDir()
	stubPath := filepath.Join(tmp, "systemctl")

	stubScript := "#!/usr/bin/env bash\nif [[ \"$2\" == \"show\" ]]; then\n  echo \"not-found\"\n  exit 0\nfi\nexit 0\n"
	if err := os.WriteFile(stubPath, []byte(stubScript), 0o755); err != nil {
		t.Fatalf("failed to write systemctl stub: %v", err)
	}

	mgr := &Manager{SystemctlBin: stubPath}
	status, err := mgr.Status("com.test.proxy", "com.test.sync")
	if err != nil {
		t.Fatalf("Status() returned error: %v", err)
	}
	if status.ProxyLoaded {
		t.Error("expected ProxyLoaded=false with not-found stub")
	}
	if status.SyncLoaded {
		t.Error("expected SyncLoaded=false with not-found stub")
	}
}
