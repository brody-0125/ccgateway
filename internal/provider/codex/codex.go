package codex

import (
	"context"
	"fmt"

	"ccgateway/internal/auth"
	"ccgateway/internal/claude"
	"ccgateway/internal/config"
	"ccgateway/internal/provider"
)

type authStrategy struct{}
type claudePatcher struct{}

func NewBundle() provider.Bundle {
	return provider.Bundle{
		VendorID:     "codex",
		RuntimeModes: []string{string(config.RuntimeModeGateway), string(config.RuntimeModeNativeCleanup)},
		Capabilities: map[provider.Capability]bool{
			provider.CapabilityAuth:   true,
			provider.CapabilityClaude: true,
		},
		Auth:   authStrategy{},
		Claude: claudePatcher{},
	}
}

func (authStrategy) Sync(_ context.Context, rt provider.ScopeRuntime) error {
	return auth.Sync(rt.Config.AuthSource, rt.Config.AuthTarget)
}

func (claudePatcher) Apply(_ context.Context, rt provider.ScopeRuntime, generation string) (claude.ApplyResult, error) {
	mode := claude.ApplyModeGateway
	token := fmt.Sprintf("ccg::%s::%s::%s", rt.Ref.VendorID, rt.Ref.ProfileID, generation)
	if rt.Config.RuntimeMode == config.RuntimeModeNativeCleanup || !rt.Config.ProxyEnabled {
		mode = claude.ApplyModeNativeCleanup
		token = ""
	}
	return claude.ApplyWithOptions(claude.ApplyOptions{
		SettingsPath: rt.Config.SettingsPath,
		SnapshotDir:  rt.Paths.SnapshotsDir,
		Port:         rt.Config.Port,
		Model:        rt.Config.Model,
		AuthToken:    token,
		Mode:         mode,
	})
}

func (claudePatcher) Revert(_ context.Context, rt provider.ScopeRuntime, snapshotPath, snapshotSHA string) error {
	return claude.Revert(rt.Config.SettingsPath, snapshotPath, snapshotSHA)
}
