package builtin

import (
	"context"
	"os"
	"testing"

	"ccgateway/internal/backend"
	"ccgateway/internal/config"
	"ccgateway/internal/scope"
)

func TestArtifactInstallCreatesExecutableWrapper(t *testing.T) {
	home := t.TempDir()
	ref := scope.MustRef("codex", "default")
	paths := scope.BuildPaths(home, home, ref)
	rt := backend.Runtime{
		Ref:   ref,
		Paths: paths,
		Config: config.DefaultForScope(home, home, ref.VendorID, ref.ProfileID),
	}
	res, err := artifactInstaller{}.Install(context.Background(), rt, "")
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if res.Version != "builtin-local" {
		t.Fatalf("unexpected version: %q", res.Version)
	}
	if res.BinaryPath != paths.ProxyBinary {
		t.Fatalf("unexpected binary path: %q", res.BinaryPath)
	}
	if res.SHA256 == "" {
		t.Fatal("expected non-empty sha256")
	}
	info, err := os.Stat(paths.ProxyBinary)
	if err != nil {
		t.Fatalf("stat wrapper failed: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatalf("wrapper is not executable: mode=%o", info.Mode())
	}
}

