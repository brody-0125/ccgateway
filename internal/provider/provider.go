package provider

import (
	"context"
	"fmt"
	"sync"

	"ccgateway/internal/claude"
	"ccgateway/internal/config"
	"ccgateway/internal/scope"
)

type Capability string

const (
	CapabilityAuth   Capability = "auth"
	CapabilityClaude Capability = "claude"
)

type ScopeRuntime struct {
	Ref    scope.Ref
	Paths  scope.Paths
	Config config.Config
}

type AuthStrategy interface {
	Sync(ctx context.Context, rt ScopeRuntime) error
}

type ClaudePatcher interface {
	Apply(ctx context.Context, rt ScopeRuntime, generation string) (claude.ApplyResult, error)
	Revert(ctx context.Context, rt ScopeRuntime, snapshotPath, snapshotSHA string) error
}

type Bundle struct {
	VendorID     string
	RuntimeModes []string
	Capabilities map[Capability]bool

	Auth   AuthStrategy
	Claude ClaudePatcher
}

func (b Bundle) SupportsMode(mode string) bool {
	for _, m := range b.RuntimeModes {
		if m == mode {
			return true
		}
	}
	return false
}

func (b Bundle) Has(cap Capability) bool {
	if b.Capabilities == nil {
		return false
	}
	return b.Capabilities[cap]
}

type Registry struct {
	mu      sync.RWMutex
	bundles map[string]Bundle
}

func NewRegistry() *Registry {
	return &Registry{bundles: map[string]Bundle{}}
}

func (r *Registry) Register(b Bundle) error {
	if b.VendorID == "" {
		return fmt.Errorf("vendor id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.bundles[b.VendorID]; exists {
		return fmt.Errorf("provider already registered: %s", b.VendorID)
	}
	r.bundles[b.VendorID] = b
	return nil
}

func (r *Registry) MustRegister(b Bundle) {
	if err := r.Register(b); err != nil {
		panic(err)
	}
}

func (r *Registry) Get(vendorID string) (Bundle, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.bundles[vendorID]
	return b, ok
}

func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.bundles))
	for id := range r.bundles {
		out = append(out, id)
	}
	return out
}
