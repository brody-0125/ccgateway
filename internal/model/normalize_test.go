package model

import "testing"

func TestNormalizeForVendorCodexAliases(t *testing.T) {
	tests := []struct {
		name  string
		raw   string
		want  string
		isErr bool
	}{
		{name: "canonical codex", raw: "gpt-5.3-codex", want: CodexModel},
		{name: "codex alias", raw: "codex", want: CodexModel},
		{name: "canonical spark", raw: "gpt-5.3-codex-spark", want: CodexSparkModel},
		{name: "spark alias codex-spark", raw: "codex-spark", want: CodexSparkModel},
		{name: "spark alias short", raw: "spark", want: CodexSparkModel},
		{name: "passthrough unknown", raw: "gpt-5.3-codex-preview", want: "gpt-5.3-codex-preview"},
		{name: "trim passthrough", raw: "  custom-model  ", want: "custom-model"},
		{name: "reject claude selector", raw: "claude-opus-4-6", isErr: true},
		{name: "reject short opus alias", raw: "opus", isErr: true},
		{name: "reject short sonnet alias", raw: "sonnet", isErr: true},
		{name: "empty", raw: "   ", isErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeForVendor("codex", tc.raw)
			if tc.isErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeForVendor()=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestNormalizeForVendorUnknownVendorPassthrough(t *testing.T) {
	got, err := NormalizeForVendor("other", "  my-model  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "my-model" {
		t.Fatalf("unexpected normalized model: %q", got)
	}
}

func TestNormalizeForVendorUnknownVendorAllowsClaudeSelectors(t *testing.T) {
	got, err := NormalizeForVendor("other", "claude-opus-4-6")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "claude-opus-4-6" {
		t.Fatalf("unexpected normalized model: %q", got)
	}
}

func TestNormalizeForVendorClaudeAliases(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "canonical opus", raw: "claude-opus-4-6", want: ClaudeOpusModel},
		{name: "short opus", raw: "opus", want: ClaudeOpusModel},
		{name: "canonical sonnet", raw: "claude-sonnet-4-6", want: ClaudeSonnetModel},
		{name: "short sonnet", raw: "sonnet", want: ClaudeSonnetModel},
		{name: "canonical haiku", raw: "claude-haiku-4-6", want: ClaudeHaikuModel},
		{name: "short haiku", raw: "haiku", want: ClaudeHaikuModel},
		{name: "passthrough", raw: "claude-opus-5-preview", want: "claude-opus-5-preview"},
	}
	for _, tc := range tests {
		got, err := NormalizeForVendor("claude", tc.raw)
		if err != nil {
			t.Fatalf("%s unexpected error: %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s NormalizeForVendor()=%q, want %q", tc.name, got, tc.want)
		}
	}
}
