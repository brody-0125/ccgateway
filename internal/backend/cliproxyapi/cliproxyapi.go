package cliproxyapi

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"ccgateway/internal/backend"
	"ccgateway/internal/launchd"
	"ccgateway/internal/proxy"
)

type artifactInstaller struct{}
type proxyRenderer struct{}
type healthChecker struct{}

func NewBundle() backend.Bundle {
	return backend.Bundle{
		ID: "cliproxyapi",
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

func (artifactInstaller) Install(_ context.Context, rt backend.Runtime, version string) (backend.ArtifactResult, error) {
	installer := proxy.NewInstaller()
	if v := os.Getenv("CCB_PROXY_API_BASE_URL"); v != "" {
		installer.RepoAPI = v
	}
	res, err := installer.Install(proxy.InstallOptions{
		Version:     version,
		Destination: rt.Paths.ProxyBinary,
		OS:          "darwin",
		Arch:        "",
	})
	if err != nil {
		return backend.ArtifactResult{}, err
	}
	return backend.ArtifactResult{Version: res.Version, SHA256: res.SHA256, BinaryPath: res.BinaryPath}, nil
}

func (proxyRenderer) WriteProxyConfig(_ context.Context, rt backend.Runtime) error {
	return launchd.WriteProxyConfig(rt.Paths.ProxyConfig, rt.Config.Port, rt.Paths.AuthDir, rt.Config.Model)
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
