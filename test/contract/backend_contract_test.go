package contract

import (
	"testing"

	"ccgateway/internal/backend"
	"ccgateway/internal/backends"
)

func TestBackendContracts(t *testing.T) {
	reg := backends.DefaultRegistry()
	ids := reg.List()
	if len(ids) == 0 {
		t.Fatal("expected at least one backend")
	}
	for _, id := range ids {
		bundle, ok := reg.Get(id)
		if !ok {
			t.Fatalf("backend not found: %s", id)
		}
		if bundle.ID == "" {
			t.Fatal("backend has empty ID")
		}
		requireBackendCapability(t, bundle, backend.CapabilityArtifact, bundle.Artifact != nil)
		requireBackendCapability(t, bundle, backend.CapabilityProxy, bundle.Proxy != nil)
		requireBackendCapability(t, bundle, backend.CapabilityHealth, bundle.Health != nil)
	}
}

func requireBackendCapability(t *testing.T, b backend.Bundle, cap backend.Capability, impl bool) {
	t.Helper()
	if b.Has(cap) && !impl {
		t.Fatalf("backend %s declares capability %s but implementation is nil", b.ID, cap)
	}
}
