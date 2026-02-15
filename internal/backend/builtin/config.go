package builtin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ServeConfig struct {
	Listen    string `json:"listen"`
	Model     string `json:"model"`
	BackendID string `json:"backend_id"`
}

func WriteConfig(path string, cfg ServeConfig) error {
	if strings.TrimSpace(cfg.Listen) == "" {
		return fmt.Errorf("listen is required")
	}
	if strings.TrimSpace(cfg.Model) == "" {
		cfg.Model = "unknown"
	}
	if strings.TrimSpace(cfg.BackendID) == "" {
		cfg.BackendID = "builtin"
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeAtomic(path, b, 0o644)
}

func LoadConfig(path string) (ServeConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ServeConfig{}, err
	}
	var cfg ServeConfig
	if err := json.Unmarshal(b, &cfg); err != nil {
		return ServeConfig{}, err
	}
	cfg.Listen = strings.TrimSpace(cfg.Listen)
	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.BackendID = strings.TrimSpace(cfg.BackendID)
	if cfg.Listen == "" {
		return ServeConfig{}, fmt.Errorf("listen is required")
	}
	if cfg.Model == "" {
		cfg.Model = "unknown"
	}
	if cfg.BackendID == "" {
		cfg.BackendID = "builtin"
	}
	return cfg, nil
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
