package settingsguard

import (
	"encoding/json"
	"fmt"
	"os"

	claudesettings "ccgateway/internal/claude"
	"ccgateway/internal/config"
	"ccgateway/internal/scope"
)

func ResolveSettingsPath(paths scope.Paths, cfg config.Config) string {
	if cfg.SettingsPath != "" {
		return cfg.SettingsPath
	}
	switch cfg.SettingsLayer {
	case config.SettingsLayerProject:
		return paths.ClaudeProjectSettingsPath
	case config.SettingsLayerLocal:
		return paths.ClaudeLocalSettingsPath
	case config.SettingsLayerUser:
		fallthrough
	default:
		return paths.ClaudeUserSettingsPath
	}
}

func CheckEffectiveOverride(paths scope.Paths, cfg config.Config) error {
	files := overrideLayers(paths, cfg.SettingsLayer)
	for _, p := range files {
		overridden, err := hasRoutingOverride(p)
		if err != nil {
			return fmt.Errorf("failed to inspect settings override in %s: %w", p, err)
		}
		if overridden {
			return fmt.Errorf("higher-priority settings override routing in %s", p)
		}
	}
	return nil
}

func overrideLayers(paths scope.Paths, target config.SettingsLayer) []string {
	switch target {
	case config.SettingsLayerUser:
		return []string{paths.ClaudeProjectSettingsPath, paths.ClaudeLocalSettingsPath}
	case config.SettingsLayerProject:
		return []string{paths.ClaudeLocalSettingsPath}
	default:
		return nil
	}
}

func hasRoutingOverride(path string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if len(b) == 0 {
		return false, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return false, err
	}
	if _, ok := doc["model"]; ok {
		return true, nil
	}
	env, ok := doc["env"].(map[string]any)
	if !ok {
		return false, nil
	}
	for _, k := range claudesettings.ManagedEnvKeys() {
		if _, ok := env[k]; ok {
			return true, nil
		}
	}
	return false, nil
}
