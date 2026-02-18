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
