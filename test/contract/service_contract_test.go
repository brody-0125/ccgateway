package contract

import (
	"testing"

	"ccgateway/internal/service"
)

func TestServiceManagerContract(t *testing.T) {
	mgr := service.NewManager()
	if mgr == nil {
		t.Fatal("service.NewManager() must return a non-nil Manager")
	}

	// Verify Status returns a valid ServiceStatus without panicking for
	// non-existent labels.
	status, err := mgr.Status("com.contract-test.nonexistent.proxy", "com.contract-test.nonexistent.sync")
	if err != nil {
		// On systems where the init system is not available (e.g., containers),
		// Status may return an error. That is acceptable.
		t.Logf("Status returned error (may be expected in containers): %v", err)
	} else {
		// When no error, ProxyLoaded and SyncLoaded should be false for
		// non-existent labels.
		if status.ProxyLoaded {
			t.Error("expected ProxyLoaded=false for non-existent label")
		}
		if status.SyncLoaded {
			t.Error("expected SyncLoaded=false for non-existent label")
		}
	}
}
