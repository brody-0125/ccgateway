package proxy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteProxyConfigIncludesCodexModelAliases(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yaml")
	if err := WriteProxyConfig(path, 8317, "/tmp/auth", "gpt-5.3-codex"); err != nil {
		t.Fatalf("write proxy config failed: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read proxy config failed: %v", err)
	}
	text := string(b)
	for _, want := range []string{
		"oauth-model-alias:",
		"codex:",
		"alias: \"opus\"",
		"alias: \"opusplan\"",
		"alias: \"sonnet\"",
		"alias: \"haiku\"",
		"alias: \"claude-opus\"",
		"alias: \"claude-sonnet\"",
		"alias: \"claude-haiku\"",
		"alias: \"claude-opus-4-6\"",
		"alias: \"claude-sonnet-4-6\"",
		"alias: \"claude-haiku-4-6\"",
		"alias: \"claude-opus-4-5\"",
		"alias: \"claude-sonnet-4-5\"",
		"alias: \"claude-haiku-4-5\"",
		"alias: \"claude-sonnet-4-5-20250929\"",
		"alias: \"claude-opus-4-5-20251101\"",
		"alias: \"claude-haiku-4-5-20251001\"",
		"name: \"gpt-5.3-codex\"",
		"name: \"gpt-*\"",
		"protocol: \"codex\"",
		"\"reasoning.effort\": \"xhigh\"",
		"\"parallel_tool_calls\": false",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("proxy config missing %q:\n%s", want, text)
		}
	}
}

func TestWriteProxyConfigFallsBackToDefaultModel(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yaml")
	if err := WriteProxyConfig(path, 8317, "/tmp/auth", ""); err != nil {
		t.Fatalf("write proxy config failed: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read proxy config failed: %v", err)
	}
	if !strings.Contains(string(b), "name: \"gpt-5.3-codex\"") {
		t.Fatalf("expected fallback model in config, got:\n%s", string(b))
	}
}

func TestWriteProxyConfigSupportsSparkModelAliases(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "config.yaml")
	if err := WriteProxyConfig(path, 8317, "/tmp/auth", "gpt-5.3-codex-spark"); err != nil {
		t.Fatalf("write proxy config failed: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read proxy config failed: %v", err)
	}
	text := string(b)
	for _, want := range []string{
		"name: \"gpt-5.3-codex-spark\"",
		"alias: \"opus\"",
		"alias: \"claude-opus-4-6\"",
		"alias: \"claude-sonnet-4-6\"",
		"alias: \"claude-haiku-4-6\"",
		"\"reasoning.effort\": \"medium\"",
		"\"parallel_tool_calls\": false",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("proxy config missing %q:\n%s", want, text)
		}
	}
}
