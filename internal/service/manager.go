// Package service defines the platform-agnostic ServiceManager interface for
// service lifecycle management (install, remove, start, stop, status).
// Platform-specific implementations are provided via build-tag files
// (manager_darwin.go for macOS launchd, manager_linux.go for Linux systemd).
package service

// ServiceStatus represents the platform-agnostic status of managed services.
type ServiceStatus struct {
	ProxyLoaded bool
	SyncLoaded  bool
}

// ServiceFiles contains all information needed for service installation.
// ProxyUnitPath and SyncUnitPath are the platform-specific unit file paths
// (plist paths on macOS, systemd unit paths on Linux), resolved by UnitPaths.
type ServiceFiles struct {
	ProxyLabel    string
	SyncLabel     string
	ProxyUnitPath string
	SyncUnitPath  string
	ProxyBinary   string
	ProxyConfig   string
	ProxyLog      string
	SyncLog       string
	SyncScript    string
	AuthSource    string
	HomeDir       string
}

// Manager defines the platform-agnostic interface for service lifecycle management.
type Manager interface {
	// Install registers and loads service units described by files.
	Install(files ServiceFiles) error

	// Remove unloads and deletes service units identified by labels and unit paths.
	Remove(proxyLabel, syncLabel, proxyUnitPath, syncUnitPath string) error

	// Start starts the proxy and sync services identified by labels.
	Start(proxyLabel, syncLabel string) error

	// Stop stops the proxy and sync services identified by labels.
	Stop(proxyLabel, syncLabel string) error

	// Status returns the current load/active status of the proxy and sync services.
	Status(proxyLabel, syncLabel string) (ServiceStatus, error)
}
