package service

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// linuxManager implements the service.Manager interface using systemd user units.
type linuxManager struct {
	systemctlBin string
}

// newLinuxManager returns a Linux systemd-backed Manager implementation.
func newLinuxManager() Manager {
	return &linuxManager{systemctlBin: systemctlBin()}
}

func (m *linuxManager) Install(files ServiceFiles) error {
	unitDir := systemdUserDir(files.HomeDir)
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return fmt.Errorf("failed to create systemd user dir: %w", err)
	}

	proxyUnit := generateProxyUnit(files)
	if err := writeAtomic(files.ProxyUnitPath, []byte(proxyUnit), 0o644); err != nil {
		return fmt.Errorf("failed to write proxy unit: %w", err)
	}

	syncUnit := generateSyncUnit(files)
	if err := writeAtomic(files.SyncUnitPath, []byte(syncUnit), 0o644); err != nil {
		_ = os.Remove(files.ProxyUnitPath)
		return fmt.Errorf("failed to write sync unit: %w", err)
	}

	if _, _, err := m.run("--user", "daemon-reload"); err != nil {
		_ = os.Remove(files.ProxyUnitPath)
		_ = os.Remove(files.SyncUnitPath)
		return fmt.Errorf("systemctl daemon-reload failed: %w", err)
	}

	if _, _, err := m.run("--user", "enable", files.SyncLabel); err != nil {
		_ = os.Remove(files.ProxyUnitPath)
		_ = os.Remove(files.SyncUnitPath)
		return fmt.Errorf("failed to enable sync unit: %w", err)
	}
	if _, _, err := m.run("--user", "enable", files.ProxyLabel); err != nil {
		_, _, _ = m.run("--user", "disable", files.SyncLabel)
		_ = os.Remove(files.ProxyUnitPath)
		_ = os.Remove(files.SyncUnitPath)
		return fmt.Errorf("failed to enable proxy unit: %w", err)
	}

	return nil
}

func (m *linuxManager) Remove(proxyLabel, syncLabel, proxyUnitPath, syncUnitPath string) error {
	_ = m.stop(proxyLabel)
	_ = m.stop(syncLabel)
	_, _, _ = m.run("--user", "disable", proxyLabel)
	_, _, _ = m.run("--user", "disable", syncLabel)
	if proxyUnitPath != "" {
		_ = os.Remove(proxyUnitPath)
	}
	if syncUnitPath != "" {
		_ = os.Remove(syncUnitPath)
	}
	_, _, _ = m.run("--user", "daemon-reload")
	return nil
}

func (m *linuxManager) Start(proxyLabel, syncLabel string) error {
	if _, _, err := m.run("--user", "start", syncLabel); err != nil {
		return fmt.Errorf("failed to start sync service: %w", err)
	}
	if _, _, err := m.run("--user", "start", proxyLabel); err != nil {
		return fmt.Errorf("failed to start proxy service: %w", err)
	}
	return nil
}

func (m *linuxManager) Stop(proxyLabel, syncLabel string) error {
	if err := m.stop(proxyLabel); err != nil {
		return err
	}
	if err := m.stop(syncLabel); err != nil {
		return err
	}
	return nil
}

func (m *linuxManager) Status(proxyLabel, syncLabel string) (ServiceStatus, error) {
	proxyLoaded, err := m.isLoaded(proxyLabel)
	if err != nil {
		return ServiceStatus{}, err
	}
	syncLoaded, err := m.isLoaded(syncLabel)
	if err != nil {
		return ServiceStatus{}, err
	}
	return ServiceStatus{
		ProxyLoaded: proxyLoaded,
		SyncLoaded:  syncLoaded,
	}, nil
}

func (m *linuxManager) isLoaded(label string) (bool, error) {
	stdout, _, err := m.run("--user", "show", label, "--property=LoadState", "--value")
	if err != nil {
		// If systemctl is not available or unit doesn't exist, treat as not loaded.
		return false, nil
	}
	return strings.TrimSpace(stdout) != "not-found", nil
}

func (m *linuxManager) stop(label string) error {
	_, stderr, err := m.run("--user", "stop", label)
	if err != nil {
		if strings.Contains(stderr, "not loaded") || strings.Contains(stderr, "not found") {
			return nil
		}
		return fmt.Errorf("failed to stop %s: %w", label, err)
	}
	return nil
}

func (m *linuxManager) run(args ...string) (string, string, error) {
	bin := m.systemctlBin
	if bin == "" {
		bin = systemctlBin()
	}
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("systemctl %v failed: %v | stderr=%s", args, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), stderr.String(), nil
}

// linuxUnitPaths resolves Linux systemd user unit file paths for the given labels.
// Falls back to defaultProxyPath/defaultSyncPath when the corresponding label is empty.
func linuxUnitPaths(homeDir, defaultProxyPath, defaultSyncPath, proxyLabel, syncLabel string) (string, string) {
	dir := systemdUserDir(homeDir)
	proxyPath := defaultProxyPath
	syncPath := defaultSyncPath
	if p := strings.TrimSpace(proxyLabel); p != "" {
		proxyPath = filepath.Join(dir, p+".service")
	}
	if s := strings.TrimSpace(syncLabel); s != "" {
		syncPath = filepath.Join(dir, s+".service")
	}
	return proxyPath, syncPath
}

func systemdUserDir(homeDir string) string {
	return filepath.Join(homeDir, ".config", "systemd", "user")
}

func systemctlBin() string {
	if v := os.Getenv("CCB_SYSTEMCTL_BIN"); v != "" {
		return v
	}
	return "systemctl"
}

func generateProxyUnit(files ServiceFiles) string {
	return fmt.Sprintf(`[Unit]
Description=ccgateway proxy (%s)
After=network.target

[Service]
Type=simple
ExecStart=%s --config %s
WorkingDirectory=%s
Restart=always
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, files.ProxyLabel, files.ProxyBinary, files.ProxyConfig, files.HomeDir, files.ProxyLog, files.ProxyLog)
}

func generateSyncUnit(files ServiceFiles) string {
	return fmt.Sprintf(`[Unit]
Description=ccgateway token-sync (%s)
After=network.target

[Service]
Type=oneshot
ExecStart=/bin/bash -lc %s
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, files.SyncLabel, files.SyncScript, files.SyncLog, files.SyncLog)
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d", path, os.Getpid())
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
