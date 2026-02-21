package service

import (
	"path/filepath"
	"strings"

	"ccgateway/internal/service/systemd"
)

// linuxManager wraps systemd.Manager to implement the service.Manager interface.
type linuxManager struct {
	mgr *systemd.Manager
}

// newLinuxManager returns a Linux systemd-backed Manager implementation.
func newLinuxManager() Manager {
	return &linuxManager{mgr: systemd.NewManager()}
}

func (m *linuxManager) Install(files ServiceFiles) error {
	return m.mgr.InstallUnits(systemd.UnitFiles{
		ProxyUnitPath: files.ProxyUnitPath,
		SyncUnitPath:  files.SyncUnitPath,
		ProxyBinary:   files.ProxyBinary,
		ProxyConfig:   files.ProxyConfig,
		ProxyLog:      files.ProxyLog,
		SyncLog:       files.SyncLog,
		SyncScript:    files.SyncScript,
		HomeDir:       files.HomeDir,
		ProxyLabel:    files.ProxyLabel,
		SyncLabel:     files.SyncLabel,
	})
}

func (m *linuxManager) Remove(proxyLabel, syncLabel, proxyUnitPath, syncUnitPath string) error {
	return m.mgr.RemoveUnits(proxyLabel, syncLabel, proxyUnitPath, syncUnitPath)
}

func (m *linuxManager) Start(proxyLabel, syncLabel string) error {
	return m.mgr.Start(proxyLabel, syncLabel)
}

func (m *linuxManager) Stop(proxyLabel, syncLabel string) error {
	return m.mgr.Stop(proxyLabel, syncLabel)
}

func (m *linuxManager) Status(proxyLabel, syncLabel string) (ServiceStatus, error) {
	st, err := m.mgr.Status(proxyLabel, syncLabel)
	if err != nil {
		return ServiceStatus{}, err
	}
	return ServiceStatus{
		ProxyLoaded: st.ProxyLoaded,
		SyncLoaded:  st.SyncLoaded,
	}, nil
}

// linuxUnitPaths resolves Linux systemd user unit file paths for the given labels.
// Falls back to defaultProxyPath/defaultSyncPath when the corresponding label is empty.
func linuxUnitPaths(homeDir, defaultProxyPath, defaultSyncPath, proxyLabel, syncLabel string) (string, string) {
	dir := systemd.UserDir(homeDir)
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
