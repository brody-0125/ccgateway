package claudevendor

import (
	"context"

	claudesettings "ccgateway/internal/claude"
	"ccgateway/internal/config"
	"ccgateway/internal/provider"
)

type claudePatcher struct{}

func NewBundle() provider.Bundle {
	return provider.Bundle{
		VendorID:     "claude",
		RuntimeModes: []string{string(config.RuntimeModeNativeDirect)},
		Capabilities: map[provider.Capability]bool{
			provider.CapabilityClaude: true,
		},
		Claude: claudePatcher{},
	}
}

func (claudePatcher) Apply(_ context.Context, rt provider.ScopeRuntime, _ string) (claudesettings.ApplyResult, error) {
	return claudesettings.ApplyWithOptions(claudesettings.ApplyOptions{
		SettingsPath: rt.Config.SettingsPath,
		SnapshotDir:  rt.Paths.SnapshotsDir,
		Port:         rt.Config.Port,
		Model:        rt.Config.Model,
		Mode:         claudesettings.ApplyModeNativeDirect,
	})
}

func (claudePatcher) Revert(_ context.Context, rt provider.ScopeRuntime, snapshotPath, snapshotSHA string) error {
	return claudesettings.Revert(rt.Config.SettingsPath, snapshotPath, snapshotSHA)
}

func (claudePatcher) SmartRevert(_ context.Context, rt provider.ScopeRuntime, snapshotPath, snapshotSHA string) error {
	return claudesettings.SmartRevert(rt.Config.SettingsPath, snapshotPath, snapshotSHA)
}
