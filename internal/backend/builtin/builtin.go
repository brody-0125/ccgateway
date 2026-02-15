package builtin

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"ccgateway/internal/backend"
	"ccgateway/internal/launchd"
	"ccgateway/internal/state"
)

type artifactInstaller struct{}
type proxyRenderer struct{}
type healthChecker struct{}

func NewBundle() backend.Bundle {
	return backend.Bundle{
		ID: "builtin",
		Capabilities: map[backend.Capability]bool{
			backend.CapabilityArtifact: true,
			backend.CapabilityProxy:    true,
			backend.CapabilityHealth:   true,
		},
		Artifact: artifactInstaller{},
		Proxy:    proxyRenderer{},
		Health:   healthChecker{},
	}
}

func (artifactInstaller) Install(_ context.Context, rt backend.Runtime, _ string) (backend.ArtifactResult, error) {
	execPath, err := os.Executable()
	if err != nil {
		return backend.ArtifactResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(rt.Paths.ProxyBinary), 0o755); err != nil {
		return backend.ArtifactResult{}, err
	}
	script := fmt.Sprintf("#!/usr/bin/env bash\nset -euo pipefail\nexec %q gateway serve \"$@\"\n", execPath)
	if err := os.WriteFile(rt.Paths.ProxyBinary, []byte(script), 0o755); err != nil {
		return backend.ArtifactResult{}, err
	}
	if err := os.Chmod(rt.Paths.ProxyBinary, 0o755); err != nil {
		return backend.ArtifactResult{}, err
	}
	sha, err := state.HashFile(rt.Paths.ProxyBinary)
	if err != nil {
		return backend.ArtifactResult{}, err
	}
	return backend.ArtifactResult{
		Version:    "builtin-local",
		SHA256:     sha,
		BinaryPath: rt.Paths.ProxyBinary,
	}, nil
}

func (proxyRenderer) WriteProxyConfig(_ context.Context, rt backend.Runtime) error {
	cfg := ServeConfig{
		Listen:    fmt.Sprintf("127.0.0.1:%d", rt.Config.Port),
		Model:     rt.Config.Model,
		BackendID: "builtin",
	}
	return WriteConfig(rt.Paths.ProxyConfig, cfg)
}

func (proxyRenderer) WriteSyncScript(_ context.Context, rt backend.Runtime, executablePath string) error {
	return launchd.WriteSyncScript(rt.Paths.SyncScriptPath, executablePath, rt.Ref.VendorID, rt.Ref.ProfileID)
}

func (healthChecker) Check(_ context.Context, rt backend.Runtime) error {
	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/v1/models", rt.Config.Port)
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("healthcheck failed: HTTP %d", resp.StatusCode)
	}
	return nil
}

