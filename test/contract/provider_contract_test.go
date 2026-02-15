package contract

import (
	"testing"

	"ccgateway/internal/provider"
	"ccgateway/internal/providers"
)

func TestProviderContracts(t *testing.T) {
	reg := providers.DefaultRegistry()
	ids := reg.List()
	if len(ids) == 0 {
		t.Fatal("expected at least one provider")
	}
	for _, id := range ids {
		bundle, ok := reg.Get(id)
		if !ok {
			t.Fatalf("provider not found: %s", id)
		}
		if bundle.VendorID == "" {
			t.Fatalf("bundle has empty VendorID")
		}
		requireCapability(t, bundle, provider.CapabilityAuth, bundle.Auth != nil)
		requireCapability(t, bundle, provider.CapabilityClaude, bundle.Claude != nil)
	}
}

func requireCapability(t *testing.T, b provider.Bundle, cap provider.Capability, impl bool) {
	t.Helper()
	if b.Has(cap) && !impl {
		t.Fatalf("provider %s declares capability %s but implementation is nil", b.VendorID, cap)
	}
}
