package service

import (
	"path/filepath"
	"strings"

	"ccgateway/internal/service/launchd"
)

// darwinManager wraps launchd.Manager to implement the service.Manager interface.
type darwinManager struct {
	mgr *launchd.Manager
}

// newDarwinManager returns a macOS launchd-backed Manager implementation.
func newDarwinManager() Manager {
	return &darwinManager{mgr: launchd.NewManager()}
}

func (d *darwinManager) Install(files ServiceFiles) error {
	return d.mgr.InstallAgents(launchd.AgentFiles{
		ProxyUnitPath: files.ProxyUnitPath,
		SyncUnitPath:  files.SyncUnitPath,
		ProxyBinary:    files.ProxyBinary,
		ProxyConfig:    files.ProxyConfig,
		ProxyLog:       files.ProxyLog,
		SyncLog:        files.SyncLog,
		SyncScript:     files.SyncScript,
		AuthSource:     files.AuthSource,
		HomeDir:        files.HomeDir,
		ProxyLabel:     files.ProxyLabel,
		SyncLabel:      files.SyncLabel,
	})
}

func (d *darwinManager) Remove(proxyLabel, syncLabel, proxyUnitPath, syncUnitPath string) error {
	return d.mgr.RemoveAgents(proxyLabel, syncLabel, proxyUnitPath, syncUnitPath)
}

func (d *darwinManager) Start(proxyLabel, syncLabel string) error {
	return d.mgr.Start(proxyLabel, syncLabel)
}

func (d *darwinManager) Stop(proxyLabel, syncLabel string) error {
	return d.mgr.Stop(proxyLabel, syncLabel)
}

func (d *darwinManager) Status(proxyLabel, syncLabel string) (ServiceStatus, error) {
	st, err := d.mgr.Status(proxyLabel, syncLabel)
	if err != nil {
		return ServiceStatus{}, err
	}
	return ServiceStatus{
		ProxyLoaded: st.ProxyLoaded,
		SyncLoaded:  st.SyncLoaded,
	}, nil
}

// darwinUnitPaths resolves macOS launchd plist file paths for the given labels.
// Falls back to defaultProxyPath/defaultSyncPath when the corresponding label is empty.
func darwinUnitPaths(homeDir, defaultProxyPath, defaultSyncPath, proxyLabel, syncLabel string) (string, string) {
	dir := filepath.Join(homeDir, "Library", "LaunchAgents")
	proxyPath := defaultProxyPath
	syncPath := defaultSyncPath
	if p := strings.TrimSpace(proxyLabel); p != "" {
		proxyPath = filepath.Join(dir, p+".plist")
	}
	if s := strings.TrimSpace(syncLabel); s != "" {
		syncPath = filepath.Join(dir, s+".plist")
	}
	return proxyPath, syncPath
}
