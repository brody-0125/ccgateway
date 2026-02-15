package backend

import (
	"context"
	"fmt"
	"sync"

	"ccgateway/internal/config"
	"ccgateway/internal/scope"
)

type Capability string

const (
	CapabilityArtifact Capability = "artifact"
	CapabilityProxy    Capability = "proxy"
	CapabilityHealth   Capability = "health"
)

type Runtime struct {
	Ref    scope.Ref
	Paths  scope.Paths
	Config config.Config
}

type ArtifactResult struct {
	Version    string
	SHA256     string
	BinaryPath string
}

type ArtifactInstaller interface {
	Install(ctx context.Context, rt Runtime, version string) (ArtifactResult, error)
}

type ProxyRenderer interface {
	WriteProxyConfig(ctx context.Context, rt Runtime) error
	WriteSyncScript(ctx context.Context, rt Runtime, executablePath string) error
}

type HealthChecker interface {
	Check(ctx context.Context, rt Runtime) error
}

type Bundle struct {
	ID           string
	Capabilities map[Capability]bool

	Artifact ArtifactInstaller
	Proxy    ProxyRenderer
	Health   HealthChecker
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
	if b.ID == "" {
		return fmt.Errorf("backend id is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.bundles[b.ID]; exists {
		return fmt.Errorf("backend already registered: %s", b.ID)
	}
	r.bundles[b.ID] = b
	return nil
}

func (r *Registry) MustRegister(b Bundle) {
	if err := r.Register(b); err != nil {
		panic(err)
	}
}

func (r *Registry) Get(id string) (Bundle, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	b, ok := r.bundles[id]
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
