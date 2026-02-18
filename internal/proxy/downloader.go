package proxy

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	cberr "ccgateway/internal/errors"
)

const DefaultRepoAPI = "https://api.github.com/repos/router-for-me/CLIProxyAPI"

type Installer struct {
	HTTPClient *http.Client
	RepoAPI    string
	UserAgent  string
}

type InstallOptions struct {
	Version     string
	Destination string
	OS          string
	Arch        string
}

type InstallResult struct {
	Version    string
	SHA256     string
	BinaryPath string
}

type releaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type releaseResponse struct {
	TagName string         `json:"tag_name"`
	Assets  []releaseAsset `json:"assets"`
}

func NewInstaller() *Installer {
	return &Installer{
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
		RepoAPI:    DefaultRepoAPI,
		UserAgent:  "ccgateway",
	}
}

func (i *Installer) Install(opts InstallOptions) (InstallResult, error) {
	if i == nil {
		i = NewInstaller()
	}
	if i.HTTPClient == nil {
		i.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	if i.RepoAPI == "" {
		i.RepoAPI = DefaultRepoAPI
	}
	if i.UserAgent == "" {
		i.UserAgent = "ccgateway"
	}

	goOS := opts.OS
	if goOS == "" {
		goOS = runtime.GOOS
	}
	goArch := opts.Arch
	if goArch == "" {
		goArch = runtime.GOARCH
	}
	if goOS != "darwin" && goOS != "linux" {
		return InstallResult{}, cberr.New(cberr.ErrInvalidArgs, "unsupported OS: "+goOS+" (supported: darwin, linux)")
	}
	assetArch := ""
	switch goArch {
	case "arm64":
		assetArch = "arm64"
	case "amd64", "x86_64":
		assetArch = "amd64"
	default:
		return InstallResult{}, cberr.New(cberr.ErrInvalidArgs, "unsupported architecture")
	}

	release, err := i.fetchRelease(opts.Version)
	if err != nil {
		return InstallResult{}, err
	}
	tarSuffix := fmt.Sprintf("%s_%s.tar.gz", goOS, assetArch)
	targetAsset, checksumsAsset := findAssets(release.Assets, tarSuffix)
	if targetAsset == nil || checksumsAsset == nil {
		return InstallResult{}, cberr.New(cberr.ErrDownloadFailed, "required release assets not found")
	}

	tmpDir, err := os.MkdirTemp("", "ccb-proxy-install-")
	if err != nil {
		return InstallResult{}, cberr.Wrap(cberr.ErrDownloadFailed, "failed to create temp dir", err)
	}
	defer os.RemoveAll(tmpDir)

	tarballPath := filepath.Join(tmpDir, targetAsset.Name)
	checksumsPath := filepath.Join(tmpDir, checksumsAsset.Name)
	if err := i.downloadFile(targetAsset.BrowserDownloadURL, tarballPath); err != nil {
		return InstallResult{}, err
	}
	if err := i.downloadFile(checksumsAsset.BrowserDownloadURL, checksumsPath); err != nil {
		return InstallResult{}, err
	}

	checksumsBytes, err := os.ReadFile(checksumsPath)
	if err != nil {
		return InstallResult{}, cberr.Wrap(cberr.ErrDownloadFailed, "failed to read checksums", err)
	}
	parsed, err := ParseChecksums(string(checksumsBytes))
	if err != nil {
		return InstallResult{}, cberr.Wrap(cberr.ErrChecksumParse, "failed to parse checksums", err)
	}
	expected, ok := parsed[targetAsset.Name]
	if !ok {
		return InstallResult{}, cberr.New(cberr.ErrChecksumParse, "target tarball checksum not found")
	}
	match, actual, err := VerifyFileChecksum(tarballPath, expected)
	if err != nil {
		return InstallResult{}, cberr.Wrap(cberr.ErrChecksumParse, "failed to hash tarball", err)
	}
	if !match {
		return InstallResult{}, cberr.New(cberr.ErrChecksumMismatch, fmt.Sprintf("checksum mismatch expected=%s actual=%s", expected, actual))
	}

	extractedPath, err := extractProxyBinary(tarballPath, tmpDir)
	if err != nil {
		return InstallResult{}, cberr.Wrap(cberr.ErrExtractFailed, "failed to extract binary", err)
	}

	if err := os.MkdirAll(filepath.Dir(opts.Destination), 0o755); err != nil {
		return InstallResult{}, cberr.Wrap(cberr.ErrDownloadFailed, "failed to prepare destination", err)
	}
	if err := atomicCopy(extractedPath, opts.Destination, 0o755); err != nil {
		return InstallResult{}, cberr.Wrap(cberr.ErrDownloadFailed, "failed to install binary", err)
	}

	return InstallResult{Version: release.TagName, SHA256: expected, BinaryPath: opts.Destination}, nil
}

func (i *Installer) fetchRelease(version string) (releaseResponse, error) {
	endpoint := "/releases/latest"
	tag := normalizeReleaseTag(version)
	if tag != "" && !strings.EqualFold(tag, "latest") {
		endpoint = "/releases/tags/" + tag
	}
	u := strings.TrimRight(i.RepoAPI, "/") + endpoint
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return releaseResponse{}, cberr.Wrap(cberr.ErrDownloadFailed, "failed to build release request", err)
	}
	req.Header.Set("User-Agent", i.UserAgent)
	resp, err := i.HTTPClient.Do(req)
	if err != nil {
		return releaseResponse{}, cberr.Wrap(cberr.ErrDownloadFailed, "release request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return releaseResponse{}, cberr.New(cberr.ErrDownloadFailed, fmt.Sprintf("release request failed: HTTP %d", resp.StatusCode))
	}
	var release releaseResponse
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return releaseResponse{}, cberr.Wrap(cberr.ErrDownloadFailed, "failed to decode release response", err)
	}
	if release.TagName == "" {
		return releaseResponse{}, cberr.New(cberr.ErrDownloadFailed, "release tag missing")
	}
	return release, nil
}

func normalizeReleaseTag(version string) string {
	v := strings.TrimSpace(version)
	if v == "" {
		return ""
	}
	if strings.EqualFold(v, "latest") {
		return "latest"
	}
	if strings.HasPrefix(v, "v") || strings.HasPrefix(v, "V") {
		return v
	}
	first := v[0]
	if first >= '0' && first <= '9' {
		return "v" + v
	}
	return v
}

func (i *Installer) downloadFile(url, dest string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return cberr.Wrap(cberr.ErrDownloadFailed, "failed to create download request", err)
	}
	req.Header.Set("User-Agent", i.UserAgent)
	resp, err := i.HTTPClient.Do(req)
	if err != nil {
		return cberr.Wrap(cberr.ErrDownloadFailed, "download request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return cberr.New(cberr.ErrDownloadFailed, fmt.Sprintf("download failed: HTTP %d", resp.StatusCode))
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return cberr.Wrap(cberr.ErrDownloadFailed, "failed to create download directory", err)
	}
	tmp := dest + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return cberr.Wrap(cberr.ErrDownloadFailed, "failed to create download file", err)
	}
	_, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return cberr.Wrap(cberr.ErrDownloadFailed, "failed to write download file", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return cberr.Wrap(cberr.ErrDownloadFailed, "failed to finalize download file", closeErr)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return cberr.Wrap(cberr.ErrDownloadFailed, "failed to move download file", err)
	}
	return nil
}

func findAssets(assets []releaseAsset, tarSuffix string) (*releaseAsset, *releaseAsset) {
	var tarAsset *releaseAsset
	var checksums *releaseAsset
	for idx := range assets {
		a := &assets[idx]
		if strings.HasSuffix(a.Name, tarSuffix) {
			tarAsset = a
		}
		if a.Name == "checksums.txt" {
			checksums = a
		}
	}
	return tarAsset, checksums
}

func extractProxyBinary(tarPath, outDir string) (string, error) {
	f, err := os.Open(tarPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		base := filepath.Base(hdr.Name)
		if base != "cli-proxy-api" && base != "CLIProxyAPI" {
			continue
		}
		dest := filepath.Join(outDir, base)
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return "", err
		}
		if err := out.Close(); err != nil {
			return "", err
		}
		if err := os.Chmod(dest, 0o755); err != nil {
			return "", err
		}
		return dest, nil
	}
	return "", fmt.Errorf("proxy binary not found in archive")
}

func atomicCopy(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := fmt.Sprintf("%s.tmp.%d.%d", dst, os.Getpid(), time.Now().UTC().UnixNano())
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
