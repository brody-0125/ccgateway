package systemd

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Manager provides low-level systemd user-unit lifecycle operations.
type Manager struct {
	SystemctlBin string
}

// ServiceStatus represents the load state of managed systemd units.
type ServiceStatus struct {
	ProxyLoaded bool
	SyncLoaded  bool
}

// UnitFiles contains all information needed for systemd unit installation.
type UnitFiles struct {
	ProxyUnitPath string
	SyncUnitPath  string
	ProxyBinary   string
	ProxyConfig   string
	ProxyLog      string
	SyncLog       string
	SyncScript    string
	HomeDir       string
	ProxyLabel    string
	SyncLabel     string
}

// NewManager returns a Manager with the default systemctl binary.
func NewManager() *Manager {
	return &Manager{SystemctlBin: systemctlBin()}
}

// InstallUnits writes unit files, reloads the daemon, and enables units.
func (m *Manager) InstallUnits(files UnitFiles) error {
	unitDir := UserDir(files.HomeDir)
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		return fmt.Errorf("failed to create systemd user dir: %w", err)
	}

	proxyUnit := GenerateProxyUnit(files)
	if err := writeAtomic(files.ProxyUnitPath, []byte(proxyUnit), 0o644); err != nil {
		return fmt.Errorf("failed to write proxy unit: %w", err)
	}

	syncUnit := GenerateSyncUnit(files)
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

// RemoveUnits stops, disables, and deletes the given units.
func (m *Manager) RemoveUnits(proxyLabel, syncLabel, proxyUnitPath, syncUnitPath string) error {
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

// Start starts the proxy and sync services.
func (m *Manager) Start(proxyLabel, syncLabel string) error {
	if _, _, err := m.run("--user", "start", syncLabel); err != nil {
		return fmt.Errorf("failed to start sync service: %w", err)
	}
	if _, _, err := m.run("--user", "start", proxyLabel); err != nil {
		return fmt.Errorf("failed to start proxy service: %w", err)
	}
	return nil
}

// Stop stops the proxy and sync services.
func (m *Manager) Stop(proxyLabel, syncLabel string) error {
	if err := m.stop(proxyLabel); err != nil {
		return err
	}
	if err := m.stop(syncLabel); err != nil {
		return err
	}
	return nil
}

// Status checks whether the proxy and sync units are loaded.
func (m *Manager) Status(proxyLabel, syncLabel string) (ServiceStatus, error) {
	proxyLoaded, err := m.IsLoaded(proxyLabel)
	if err != nil {
		return ServiceStatus{}, err
	}
	syncLoaded, err := m.IsLoaded(syncLabel)
	if err != nil {
		return ServiceStatus{}, err
	}
	return ServiceStatus{ProxyLoaded: proxyLoaded, SyncLoaded: syncLoaded}, nil
}

// IsLoaded returns true if the given unit is found by systemd.
func (m *Manager) IsLoaded(label string) (bool, error) {
	stdout, _, err := m.run("--user", "show", label, "--property=LoadState", "--value")
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(stdout) != "not-found", nil
}

func (m *Manager) stop(label string) error {
	_, stderr, err := m.run("--user", "stop", label)
	if err != nil {
		if strings.Contains(stderr, "not loaded") || strings.Contains(stderr, "not found") {
			return nil
		}
		return fmt.Errorf("failed to stop %s: %w", label, err)
	}
	return nil
}

func (m *Manager) run(args ...string) (string, string, error) {
	bin := m.SystemctlBin
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

// GenerateProxyUnit returns the systemd unit file content for the proxy service.
func GenerateProxyUnit(files UnitFiles) string {
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

// GenerateSyncUnit returns the systemd unit file content for the token-sync service.
func GenerateSyncUnit(files UnitFiles) string {
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

// UserDir returns the systemd user unit directory path.
func UserDir(homeDir string) string {
	return filepath.Join(homeDir, ".config", "systemd", "user")
}

func systemctlBin() string {
	if v := os.Getenv("CCG_SYSTEMCTL_BIN"); v != "" {
		return v
	}
	return "systemctl"
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", path, os.Getpid(), time.Now().UTC().UnixNano())
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
