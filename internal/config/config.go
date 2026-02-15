package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultPort           = 8317
	DefaultGatewayBackend = "cliproxyapi"
)

type RuntimeMode string

type SettingsLayer string

const (
	RuntimeModeGateway       RuntimeMode = "gateway"
	RuntimeModeNativeCleanup RuntimeMode = "native-cleanup"
	RuntimeModeNativeDirect  RuntimeMode = "native-direct"
	// RuntimeModeNative is a legacy alias preserved for compatibility.
	RuntimeModeNative RuntimeMode = RuntimeModeNativeCleanup

	SettingsLayerUser    SettingsLayer = "user"
	SettingsLayerProject SettingsLayer = "project"
	SettingsLayerLocal   SettingsLayer = "local"
)

type Config struct {
	SchemaVersion int

	VendorID  string
	ProfileID string

	RuntimeMode    RuntimeMode
	PolicyMode     string
	Port           int
	Model          string
	AuthMode       string
	ProxyEnabled   bool
	ProxyVersion   string
	GatewayBackend string

	AuthSource string
	AuthTarget string

	SettingsLayer SettingsLayer
	SettingsPath  string
}

func Default(home string) Config {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	return DefaultForScope(home, cwd, "codex", "default")
}

func DefaultForScope(home, cwd, vendorID, profileID string) Config {
	if strings.TrimSpace(vendorID) == "" {
		vendorID = "codex"
	}
	if strings.TrimSpace(profileID) == "" {
		profileID = "default"
	}
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	cfg := Config{
		SchemaVersion:  2,
		VendorID:       vendorID,
		ProfileID:      profileID,
		RuntimeMode:    RuntimeModeGateway,
		PolicyMode:     "strict",
		Port:           DefaultPort,
		Model:          "gpt-5.3-codex",
		AuthMode:       "oauth_file",
		ProxyEnabled:   true,
		ProxyVersion:   "latest",
		GatewayBackend: DefaultGatewayBackend,
		AuthSource:     filepath.Join(home, ".codex", "auth.json"),
		AuthTarget:     filepath.Join(home, ".ccgateway", "auths", "codex-from-codex-cli.json"),
		SettingsLayer:  SettingsLayerProject,
		SettingsPath:   filepath.Join(cwd, ".claude", "settings.json"),
	}
	if strings.EqualFold(vendorID, "claude") {
		cfg.RuntimeMode = RuntimeModeNativeDirect
		cfg.PolicyMode = "compat"
		cfg.ProxyEnabled = false
		cfg.Model = "claude-opus-4-6"
		cfg.AuthMode = "native_direct"
		cfg.AuthSource = ""
		cfg.AuthTarget = ""
	}
	return cfg
}

func Load(path, home string) (Config, error) {
	cfg := Default(home)
	return LoadWithDefault(path, cfg)
}

func LoadWithDefault(path string, defaults Config) (Config, error) {
	cfg := defaults
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.TrimSpace(parts[0])
		v := strings.Trim(strings.TrimSpace(parts[1]), `"'`)

		switch k {
		case "schema_version":
			n, convErr := strconv.Atoi(v)
			if convErr != nil || n <= 0 {
				return cfg, fmt.Errorf("invalid schema_version: %q", v)
			}
			cfg.SchemaVersion = n
		case "vendor_id":
			if v != "" {
				cfg.VendorID = v
			}
		case "profile_id":
			if v != "" {
				cfg.ProfileID = v
			}
		case "runtime_mode":
			if mode, ok := ParseRuntimeMode(v); ok {
				cfg.RuntimeMode = mode
			}
		case "policy_mode":
			if v != "" {
				cfg.PolicyMode = strings.ToLower(v)
			}
		case "port":
			n, convErr := strconv.Atoi(v)
			if convErr != nil || n <= 0 || n > 65535 {
				return cfg, fmt.Errorf("invalid port: %q", v)
			}
			cfg.Port = n
		case "model":
			if v != "" {
				cfg.Model = v
			}
		case "auth_mode":
			if v != "" {
				cfg.AuthMode = v
			}
		case "proxy_enabled":
			b, convErr := strconv.ParseBool(v)
			if convErr != nil {
				return cfg, fmt.Errorf("invalid proxy_enabled: %q", v)
			}
			cfg.ProxyEnabled = b
		case "proxy_version":
			if v != "" {
				cfg.ProxyVersion = v
			}
		case "gateway_backend":
			if v != "" {
				cfg.GatewayBackend = v
			}
		case "auth_source":
			if v != "" {
				cfg.AuthSource = v
			}
		case "auth_target":
			if v != "" {
				cfg.AuthTarget = v
			}
		case "settings_layer":
			switch SettingsLayer(v) {
			case SettingsLayerUser, SettingsLayerProject, SettingsLayerLocal:
				cfg.SettingsLayer = SettingsLayer(v)
			}
		case "settings_path":
			if v != "" {
				cfg.SettingsPath = v
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return cfg, err
	}
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = 2
	}
	if cfg.GatewayBackend == "" {
		cfg.GatewayBackend = DefaultGatewayBackend
	}
	if cfg.PolicyMode == "" {
		if strings.EqualFold(cfg.VendorID, "codex") {
			cfg.PolicyMode = "strict"
		} else {
			cfg.PolicyMode = "compat"
		}
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if cfg.SchemaVersion == 0 {
		cfg.SchemaVersion = 2
	}
	if cfg.RuntimeMode == "" {
		cfg.RuntimeMode = RuntimeModeGateway
	} else if mode, ok := ParseRuntimeMode(string(cfg.RuntimeMode)); ok {
		cfg.RuntimeMode = mode
	}
	if cfg.SettingsLayer == "" {
		cfg.SettingsLayer = SettingsLayerProject
	}
	if cfg.PolicyMode == "" {
		if strings.EqualFold(cfg.VendorID, "codex") {
			cfg.PolicyMode = "strict"
		} else {
			cfg.PolicyMode = "compat"
		}
	}
	if cfg.GatewayBackend == "" {
		cfg.GatewayBackend = DefaultGatewayBackend
	}
	body := fmt.Sprintf(
		"schema_version: %d\nvendor_id: %q\nprofile_id: %q\nruntime_mode: %q\npolicy_mode: %q\nport: %d\nmodel: %q\nauth_mode: %q\nproxy_enabled: %t\nproxy_version: %q\ngateway_backend: %q\nauth_source: %q\nauth_target: %q\nsettings_layer: %q\nsettings_path: %q\n",
		cfg.SchemaVersion,
		cfg.VendorID,
		cfg.ProfileID,
		cfg.RuntimeMode,
		cfg.PolicyMode,
		cfg.Port,
		cfg.Model,
		cfg.AuthMode,
		cfg.ProxyEnabled,
		cfg.ProxyVersion,
		cfg.GatewayBackend,
		cfg.AuthSource,
		cfg.AuthTarget,
		cfg.SettingsLayer,
		cfg.SettingsPath,
	)
	return writeAtomic(path, []byte(body), 0o644)
}

func ExpandHome(input, home string) string {
	if input == "~" {
		return home
	}
	if strings.HasPrefix(input, "~/") {
		return filepath.Join(home, strings.TrimPrefix(input, "~/"))
	}
	return input
}

// ParseRuntimeMode normalizes runtime mode input and supports legacy aliases.
func ParseRuntimeMode(v string) (RuntimeMode, bool) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case string(RuntimeModeGateway):
		return RuntimeModeGateway, true
	case "native", string(RuntimeModeNativeCleanup):
		return RuntimeModeNativeCleanup, true
	case string(RuntimeModeNativeDirect):
		return RuntimeModeNativeDirect, true
	default:
		return "", false
	}
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", path, os.Getpid(), time.Now().UTC().UnixNano())
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
