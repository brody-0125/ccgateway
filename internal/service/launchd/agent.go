package launchd

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	modelnorm "ccgateway/internal/model"
)

type Manager struct {
	UID          int
	LaunchctlBin string
}

type ServiceStatus struct {
	ProxyLoaded bool
	SyncLoaded  bool
}

type AgentFiles struct {
	ProxyPlistPath string
	SyncPlistPath  string
	ProxyBinary    string
	ProxyConfig    string
	ProxyLog       string
	SyncLog        string
	SyncScript     string
	AuthSource     string
	HomeDir        string
	ProxyLabel     string
	SyncLabel      string
}

func NewManager() *Manager {
	return &Manager{UID: os.Getuid(), LaunchctlBin: launchctlBin()}
}

func LabelsForUser(username string) (string, string) {
	if strings.TrimSpace(username) == "" {
		username = strconv.Itoa(os.Getuid())
	}
	proxy := fmt.Sprintf("com.%s.ccgateway.proxy", username)
	sync := fmt.Sprintf("com.%s.ccgateway.token-sync", username)
	return proxy, sync
}

func (m *Manager) InstallAgents(files AgentFiles) error {
	cleanupOnFailure := func(cause error) error {
		return m.cleanupInstallFailure(files, cause)
	}
	if err := os.MkdirAll(filepath.Dir(files.ProxyPlistPath), 0o755); err != nil {
		return err
	}
	if err := writeAtomic(files.SyncPlistPath, []byte(syncPlist(files)), 0o644); err != nil {
		return cleanupOnFailure(fmt.Errorf("failed to write sync plist: %w", err))
	}
	if err := writeAtomic(files.ProxyPlistPath, []byte(proxyPlist(files)), 0o644); err != nil {
		return cleanupOnFailure(fmt.Errorf("failed to write proxy plist: %w", err))
	}
	if err := m.Bootout(files.ProxyLabel); err != nil && !containsNotLoaded(err.Error()) {
		return cleanupOnFailure(err)
	}
	if err := m.Bootout(files.SyncLabel); err != nil && !containsNotLoaded(err.Error()) {
		return cleanupOnFailure(err)
	}
	if err := m.Bootstrap(files.SyncPlistPath); err != nil {
		return cleanupOnFailure(err)
	}
	if err := m.Bootstrap(files.ProxyPlistPath); err != nil {
		return cleanupOnFailure(err)
	}
	return nil
}

func (m *Manager) cleanupInstallFailure(files AgentFiles, cause error) error {
	if cause == nil {
		return nil
	}
	var cleanupErrs []error

	if err := m.Bootout(files.ProxyLabel); err != nil && !containsNotLoaded(err.Error()) {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("cleanup bootout proxy failed: %w", err))
	}
	if err := m.Bootout(files.SyncLabel); err != nil && !containsNotLoaded(err.Error()) {
		cleanupErrs = append(cleanupErrs, fmt.Errorf("cleanup bootout sync failed: %w", err))
	}
	if files.ProxyPlistPath != "" {
		if err := os.Remove(files.ProxyPlistPath); err != nil && !os.IsNotExist(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("cleanup remove proxy plist failed: %w", err))
		}
	}
	if files.SyncPlistPath != "" {
		if err := os.Remove(files.SyncPlistPath); err != nil && !os.IsNotExist(err) {
			cleanupErrs = append(cleanupErrs, fmt.Errorf("cleanup remove sync plist failed: %w", err))
		}
	}
	if len(cleanupErrs) == 0 {
		return cause
	}
	return fmt.Errorf("%w; cleanup failed: %v", cause, errors.Join(cleanupErrs...))
}

func (m *Manager) RemoveAgents(proxyLabel, syncLabel, proxyPlistPath, syncPlistPath string) error {
	if err := m.Bootout(proxyLabel); err != nil && !containsNotLoaded(err.Error()) {
		return err
	}
	if err := m.Bootout(syncLabel); err != nil && !containsNotLoaded(err.Error()) {
		return err
	}
	if proxyPlistPath != "" {
		_ = os.Remove(proxyPlistPath)
	}
	if syncPlistPath != "" {
		_ = os.Remove(syncPlistPath)
	}
	return nil
}

func (m *Manager) Start(proxyLabel, syncLabel string) error {
	if err := m.Kickstart(syncLabel); err != nil {
		return err
	}
	if err := m.Kickstart(proxyLabel); err != nil {
		return err
	}
	return nil
}

func (m *Manager) Stop(proxyLabel, syncLabel string) error {
	if err := m.Bootout(proxyLabel); err != nil && !containsNotLoaded(err.Error()) {
		return err
	}
	if err := m.Bootout(syncLabel); err != nil && !containsNotLoaded(err.Error()) {
		return err
	}
	return nil
}

func (m *Manager) Status(proxyLabel, syncLabel string) (ServiceStatus, error) {
	proxyLoaded, err := m.Print(proxyLabel)
	if err != nil {
		return ServiceStatus{}, err
	}
	syncLoaded, err := m.Print(syncLabel)
	if err != nil {
		return ServiceStatus{}, err
	}
	return ServiceStatus{ProxyLoaded: proxyLoaded, SyncLoaded: syncLoaded}, nil
}

func (m *Manager) Bootstrap(plistPath string) error {
	_, _, err := m.run("bootstrap", fmt.Sprintf("gui/%d", m.UID), plistPath)
	return err
}

func (m *Manager) Bootout(label string) error {
	_, _, err := m.run("bootout", fmt.Sprintf("gui/%d/%s", m.UID, label))
	return err
}

func (m *Manager) Kickstart(label string) error {
	_, _, err := m.run("kickstart", "-k", fmt.Sprintf("gui/%d/%s", m.UID, label))
	return err
}

func (m *Manager) Print(label string) (bool, error) {
	_, stderr, err := m.run("print", fmt.Sprintf("gui/%d/%s", m.UID, label))
	if err != nil {
		if containsNotLoaded(stderr) || containsNotLoaded(err.Error()) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (m *Manager) run(args ...string) (string, string, error) {
	bin := m.LaunchctlBin
	if bin == "" {
		bin = launchctlBin()
	}
	cmd := exec.Command(bin, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("launchctl %v failed: %v | stderr=%s", args, err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), stderr.String(), nil
}

func WriteProxyConfig(path string, port int, authDir, model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		model = modelnorm.CodexModel
	}
	reasoningEffort := "xhigh"
	if strings.EqualFold(model, modelnorm.CodexSparkModel) {
		// Spark is tuned for low-latency interactions; lower reasoning budget reduces malformed tool-call spikes.
		reasoningEffort = "medium"
	}
	body := fmt.Sprintf(
		"port: %d\nauth-dir: %q\n\noauth-model-alias:\n  codex:\n%s\npayload:\n  override:\n    - models:\n        - name: \"gpt-*\"\n          protocol: \"codex\"\n      params:\n        \"reasoning.effort\": %q\n        \"parallel_tool_calls\": false\n",
		port,
		authDir,
		codexAliasSection(model),
		reasoningEffort,
	)
	return writeAtomic(path, []byte(body), 0o644)
}

func codexAliasSection(model string) string {
	aliases := []string{
		"opus",
		"opusplan",
		"sonnet",
		"haiku",
		"claude-opus",
		"claude-sonnet",
		"claude-haiku",
		"claude-opus-4-6",
		"claude-sonnet-4-6",
		"claude-haiku-4-6",
		"claude-opus-4-5",
		"claude-sonnet-4-5",
		"claude-haiku-4-5",
		"claude-opus-4-5-20251101",
		"claude-sonnet-4-5-20250929",
		"claude-haiku-4-5-20251001",
	}
	var b strings.Builder
	for _, alias := range aliases {
		_, _ = fmt.Fprintf(&b, "    - name: %q\n      alias: %q\n      fork: true\n", model, alias)
	}
	return b.String()
}

func WriteSyncScript(path, executablePath, vendorID, profileID string) error {
	body := fmt.Sprintf("#!/usr/bin/env bash\nset -euo pipefail\n\n%q auth sync --vendor %q --profile %q\n", executablePath, vendorID, profileID)
	return writeAtomic(path, []byte(body), 0o755)
}

func syncPlist(files AgentFiles) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>/bin/bash</string>
    <string>-lc</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>WatchPaths</key>
  <array>
    <string>%s</string>
  </array>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, files.SyncLabel, files.SyncScript, files.AuthSource, files.SyncLog, files.SyncLog)
}

func proxyPlist(files AgentFiles) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>--config</string>
    <string>%s</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>WorkingDirectory</key><string>%s</string>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, files.ProxyLabel, files.ProxyBinary, files.ProxyConfig, files.HomeDir, files.ProxyLog, files.ProxyLog)
}

func launchctlBin() string {
	if v := os.Getenv("CCB_LAUNCHCTL_BIN"); v != "" {
		return v
	}
	return "launchctl"
}

func containsNotLoaded(s string) bool {
	s = strings.ToLower(s)
	return strings.Contains(s, "could not find service") || strings.Contains(s, "no such process") || strings.Contains(s, "service is not loaded")
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", path, os.Getpid(), time.Now().UTC().UnixNano())
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
