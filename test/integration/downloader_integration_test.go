package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	cberr "ccgateway/internal/errors"
	"ccgateway/internal/proxy"
)

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

func TestDownloaderSuccessAndChecksumMismatch(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		runDownloaderCase(t, false)
	})
	t.Run("mismatch", func(t *testing.T) {
		runDownloaderCase(t, true)
	})
}

func runDownloaderCase(t *testing.T, mismatch bool) {
	tarball := makeTarball(t)
	hash := sha256.Sum256(tarball)
	hashHex := hex.EncodeToString(hash[:])

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	defer server.Close()

	tarName := "CLIProxyAPI_1.2.3_darwin_arm64.tar.gz"
	checksums := hashHex + "  " + tarName + "\n"
	if mismatch {
		checksums = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff  " + tarName + "\n"
	}

	rel := release{TagName: "v1.2.3", Assets: []asset{
		{Name: tarName, BrowserDownloadURL: server.URL + "/assets/tar"},
		{Name: "checksums.txt", BrowserDownloadURL: server.URL + "/assets/checksums"},
	}}

	mux.HandleFunc("/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(rel)
	})
	mux.HandleFunc("/assets/tar", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tarball)
	})
	mux.HandleFunc("/assets/checksums", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(checksums))
	})

	dir := t.TempDir()
	dest := filepath.Join(dir, "cli-proxy-api")
	inst := proxy.NewInstaller()
	inst.RepoAPI = server.URL
	res, err := inst.Install(proxy.InstallOptions{Version: "latest", Destination: dest, OS: "darwin", Arch: "arm64"})
	if mismatch {
		if err == nil {
			t.Fatal("expected checksum mismatch error")
		}
		if cberr.Code(err) != cberr.ErrChecksumMismatch {
			t.Fatalf("expected %s, got %s (%v)", cberr.ErrChecksumMismatch, cberr.Code(err), err)
		}
		return
	}
	if err != nil {
		t.Fatalf("install failed: %v", err)
	}
	if res.SHA256 == "" || res.Version == "" {
		t.Fatalf("unexpected install result: %#v", res)
	}
	if _, statErr := os.Stat(dest); statErr != nil {
		t.Fatalf("expected destination binary: %v", statErr)
	}
}

func TestDownloaderNetworkFailure(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "cli-proxy-api")
	inst := proxy.NewInstaller()
	inst.RepoAPI = "http://127.0.0.1:1"
	_, err := inst.Install(proxy.InstallOptions{Version: "latest", Destination: dest, OS: "darwin", Arch: "arm64"})
	if err == nil {
		t.Fatal("expected error")
	}
	if cberr.Code(err) != cberr.ErrDownloadFailed {
		t.Fatalf("expected %s, got %s (%v)", cberr.ErrDownloadFailed, cberr.Code(err), err)
	}
}

func makeTarball(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	content := []byte("#!/usr/bin/env bash\necho hello\n")
	h := &tar.Header{
		Name: "cli-proxy-api",
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(h); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("write body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}
