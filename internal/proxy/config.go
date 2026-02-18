package proxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	modelnorm "ccgateway/internal/model"
)

// WriteProxyConfig writes the proxy configuration file for the given
// port, auth directory, and model.
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
	return writeAtomicConfig(path, []byte(body), 0o644)
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

// WriteSyncScript writes the token-sync shell script.
func WriteSyncScript(path, executablePath, vendorID, profileID string) error {
	body := fmt.Sprintf("#!/usr/bin/env bash\nset -euo pipefail\n\n%q auth sync --vendor %q --profile %q\n", executablePath, vendorID, profileID)
	return writeAtomicConfig(path, []byte(body), 0o755)
}

func writeAtomicConfig(path string, data []byte, mode os.FileMode) error {
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
