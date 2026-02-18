package proxy

import (
	"testing"

	cberr "ccgateway/internal/errors"
)

func TestNormalizeReleaseTag(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "", want: ""},
		{in: "latest", want: "latest"},
		{in: "LATEST", want: "latest"},
		{in: "v6.8.15", want: "v6.8.15"},
		{in: "V6.8.15", want: "V6.8.15"},
		{in: "6.8.15", want: "v6.8.15"},
		{in: " 6.8.15 ", want: "v6.8.15"},
		{in: "release-foo", want: "release-foo"},
	}

	for _, tc := range tests {
		got := normalizeReleaseTag(tc.in)
		if got != tc.want {
			t.Fatalf("normalizeReleaseTag(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestInstallRejectsUnsupportedOS(t *testing.T) {
	installer := NewInstaller()
	_, err := installer.Install(InstallOptions{
		Version:     "latest",
		Destination: "/tmp/proxy",
		OS:          "windows",
	})
	if err == nil {
		t.Fatal("expected error for unsupported OS")
	}
	if code := cberr.Code(err); code != cberr.ErrInvalidArgs {
		t.Fatalf("expected ErrInvalidArgs, got %v", code)
	}
}

func TestInstallAcceptsDarwin(t *testing.T) {
	installer := &Installer{
		RepoAPI:   "http://127.0.0.1:1/nonexistent",
		UserAgent: "test",
	}
	// Will fail at network level but should pass the OS validation
	_, err := installer.Install(InstallOptions{
		Version:     "latest",
		Destination: "/tmp/proxy",
		OS:          "darwin",
		Arch:        "arm64",
	})
	if err == nil {
		t.Fatal("expected network error, not nil")
	}
	if code := cberr.Code(err); code == cberr.ErrInvalidArgs {
		t.Fatalf("darwin should be accepted, got ErrInvalidArgs: %v", err)
	}
}

func TestInstallAcceptsLinux(t *testing.T) {
	installer := &Installer{
		RepoAPI:   "http://127.0.0.1:1/nonexistent",
		UserAgent: "test",
	}
	// Will fail at network level but should pass the OS validation
	_, err := installer.Install(InstallOptions{
		Version:     "latest",
		Destination: "/tmp/proxy",
		OS:          "linux",
		Arch:        "amd64",
	})
	if err == nil {
		t.Fatal("expected network error, not nil")
	}
	if code := cberr.Code(err); code == cberr.ErrInvalidArgs {
		t.Fatalf("linux should be accepted, got ErrInvalidArgs: %v", err)
	}
}

func TestFindAssetsMatchesLinuxSuffix(t *testing.T) {
	assets := []releaseAsset{
		{Name: "cli-proxy-api_0.1.0_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.com/darwin_arm64.tar.gz"},
		{Name: "cli-proxy-api_0.1.0_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux_amd64.tar.gz"},
		{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
	}

	tar, checksums := findAssets(assets, "linux_amd64.tar.gz")
	if tar == nil {
		t.Fatal("expected linux tar asset to be found")
	}
	if tar.Name != "cli-proxy-api_0.1.0_linux_amd64.tar.gz" {
		t.Fatalf("unexpected tar name: %s", tar.Name)
	}
	if checksums == nil {
		t.Fatal("expected checksums asset to be found")
	}
}

func TestFindAssetsMatchesDarwinSuffix(t *testing.T) {
	assets := []releaseAsset{
		{Name: "cli-proxy-api_0.1.0_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.com/darwin_arm64.tar.gz"},
		{Name: "cli-proxy-api_0.1.0_linux_amd64.tar.gz", BrowserDownloadURL: "https://example.com/linux_amd64.tar.gz"},
		{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
	}

	tar, checksums := findAssets(assets, "darwin_arm64.tar.gz")
	if tar == nil {
		t.Fatal("expected darwin tar asset to be found")
	}
	if tar.Name != "cli-proxy-api_0.1.0_darwin_arm64.tar.gz" {
		t.Fatalf("unexpected tar name: %s", tar.Name)
	}
	if checksums == nil {
		t.Fatal("expected checksums asset to be found")
	}
}

func TestFindAssetsReturnsNilForMissingSuffix(t *testing.T) {
	assets := []releaseAsset{
		{Name: "cli-proxy-api_0.1.0_darwin_arm64.tar.gz", BrowserDownloadURL: "https://example.com/darwin_arm64.tar.gz"},
		{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
	}

	tar, _ := findAssets(assets, "linux_amd64.tar.gz")
	if tar != nil {
		t.Fatalf("expected nil for missing linux asset, got: %s", tar.Name)
	}
}
