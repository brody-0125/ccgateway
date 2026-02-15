package scope

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var scopePartPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type Ref struct {
	VendorID  string
	ProfileID string
}

type Paths struct {
	Home string
	Cwd  string

	BaseDir      string
	GlobalDir    string
	ScopeDir     string
	ConfigPath   string
	StatePath    string
	AuthDir      string
	AuthSource   string
	AuthTarget   string
	LogsDir      string
	AppLogPath   string
	ProxyLogPath string
	SyncLogPath  string

	SnapshotsDir   string
	ProxyDir       string
	ProxyBinary    string
	ProxyConfig    string
	LaunchdDir     string
	SyncScriptPath string

	LaunchAgentDir string
	ProxyPlistPath string
	SyncPlistPath  string

	ClaudeUserSettingsPath    string
	ClaudeProjectSettingsPath string
	ClaudeLocalSettingsPath   string

	ActivePath string
	SwitchLock string
	PortsPath  string
}

func NewRef(vendorID, profileID string) (Ref, error) {
	v := normalizeScopePart(vendorID)
	p := normalizeScopePart(profileID)
	if !scopePartPattern.MatchString(v) {
		return Ref{}, fmt.Errorf("invalid vendor_id: %q", vendorID)
	}
	if !scopePartPattern.MatchString(p) {
		return Ref{}, fmt.Errorf("invalid profile_id: %q", profileID)
	}
	return Ref{VendorID: v, ProfileID: p}, nil
}

func MustRef(vendorID, profileID string) Ref {
	ref, err := NewRef(vendorID, profileID)
	if err != nil {
		panic(err)
	}
	return ref
}

func (r Ref) ScopeID() string {
	return r.VendorID + ":" + r.ProfileID
}

func (r Ref) LabelBase(username string) string {
	u := normalizeScopePart(username)
	if u == "" {
		u = fmt.Sprintf("uid%d", os.Getuid())
	}
	return fmt.Sprintf("com.%s.ccb.%s.%s", u, r.VendorID, r.ProfileID)
}

func (r Ref) Labels(username string) (proxyLabel, syncLabel string) {
	base := r.LabelBase(username)
	return base + ".proxy", base + ".sync"
}

func BuildPaths(home, cwd string, r Ref) Paths {
	base := filepath.Join(home, ".ccgateway")
	scopeDir := filepath.Join(base, "vendors", r.VendorID, "profiles", r.ProfileID)
	launchAgentDir := filepath.Join(home, "Library", "LaunchAgents")
	proxyLabel, syncLabel := r.Labels(currentUser())

	return Paths{
		Home: home,
		Cwd:  cwd,

		BaseDir:    base,
		GlobalDir:  filepath.Join(base, "global"),
		ScopeDir:   scopeDir,
		ConfigPath: filepath.Join(scopeDir, "config.yaml"),
		StatePath:  filepath.Join(scopeDir, "state.json"),

		AuthDir:    filepath.Join(scopeDir, "auth"),
		AuthSource: filepath.Join(home, ".codex", "auth.json"),
		AuthTarget: filepath.Join(scopeDir, "auth", "codex-from-codex-cli.json"),

		LogsDir:      filepath.Join(scopeDir, "logs"),
		AppLogPath:   filepath.Join(scopeDir, "logs", "app.log"),
		ProxyLogPath: filepath.Join(scopeDir, "logs", "proxy.log"),
		SyncLogPath:  filepath.Join(scopeDir, "logs", "token-sync.log"),

		SnapshotsDir:   filepath.Join(scopeDir, "snapshots"),
		ProxyDir:       filepath.Join(scopeDir, "proxy"),
		ProxyBinary:    filepath.Join(scopeDir, "proxy", "cli-proxy-api"),
		ProxyConfig:    filepath.Join(scopeDir, "proxy", "config.yaml"),
		LaunchdDir:     filepath.Join(scopeDir, "launchd"),
		SyncScriptPath: filepath.Join(scopeDir, "launchd", "sync.sh"),

		LaunchAgentDir: launchAgentDir,
		ProxyPlistPath: filepath.Join(launchAgentDir, proxyLabel+".plist"),
		SyncPlistPath:  filepath.Join(launchAgentDir, syncLabel+".plist"),

		ClaudeUserSettingsPath:    filepath.Join(home, ".claude", "settings.json"),
		ClaudeProjectSettingsPath: filepath.Join(cwd, ".claude", "settings.json"),
		ClaudeLocalSettingsPath:   filepath.Join(cwd, ".claude", "settings.local.json"),

		ActivePath: filepath.Join(base, "global", "active.json"),
		SwitchLock: filepath.Join(base, "global", "locks", "switch.lock"),
		PortsPath:  filepath.Join(base, "global", "ports.json"),
	}
}

func ScopeFromID(scopeID string) (Ref, error) {
	parts := strings.Split(scopeID, ":")
	if len(parts) != 2 {
		return Ref{}, fmt.Errorf("invalid scope id: %q", scopeID)
	}
	return NewRef(parts[0], parts[1])
}

func normalizeScopePart(v string) string {
	v = strings.TrimSpace(strings.ToLower(v))
	v = strings.ReplaceAll(v, "_", "-")
	v = strings.ReplaceAll(v, " ", "-")
	v = strings.Trim(v, "-")
	for strings.Contains(v, "--") {
		v = strings.ReplaceAll(v, "--", "-")
	}
	return v
}

func currentUser() string {
	if v := strings.TrimSpace(os.Getenv("USER")); v != "" {
		return v
	}
	return fmt.Sprintf("uid%d", os.Getuid())
}
