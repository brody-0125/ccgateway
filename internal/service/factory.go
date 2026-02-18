package service

import "runtime"

// NewManager returns a platform-appropriate Manager implementation.
// On darwin it returns a launchd-backed manager; on linux it returns a
// systemd-backed manager.
func NewManager() Manager {
	switch runtime.GOOS {
	case "darwin":
		return newDarwinManager()
	default:
		return newLinuxManager()
	}
}

// UnitPaths resolves platform-appropriate unit file paths for the given labels.
// On darwin the paths point to ~/Library/LaunchAgents/*.plist; on linux they
// point to ~/.config/systemd/user/*.service.
func UnitPaths(homeDir, defaultProxyPath, defaultSyncPath, proxyLabel, syncLabel string) (string, string) {
	switch runtime.GOOS {
	case "darwin":
		return darwinUnitPaths(homeDir, defaultProxyPath, defaultSyncPath, proxyLabel, syncLabel)
	default:
		return linuxUnitPaths(homeDir, defaultProxyPath, defaultSyncPath, proxyLabel, syncLabel)
	}
}
