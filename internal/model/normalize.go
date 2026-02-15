package model

import (
	"fmt"
	"strings"
)

const (
	CodexModel      = "gpt-5.3-codex"
	CodexSparkModel = "gpt-5.3-codex-spark"

	ClaudeOpusModel   = "claude-opus-4-6"
	ClaudeSonnetModel = "claude-sonnet-4-6"
	ClaudeHaikuModel  = "claude-haiku-4-6"
)

// NormalizeForVendor normalizes user-facing model aliases into canonical IDs.
// Unknown non-empty models are preserved as-is, except vendor-policy disallowed selectors.
func NormalizeForVendor(vendorID, raw string) (string, error) {
	model := strings.TrimSpace(raw)
	if model == "" {
		return "", fmt.Errorf("model is required")
	}

	if !strings.EqualFold(strings.TrimSpace(vendorID), "codex") {
		if !strings.EqualFold(strings.TrimSpace(vendorID), "claude") {
			return model, nil
		}
		switch strings.ToLower(model) {
		case "claude-opus-4-6", "claude-opus", "opus":
			return ClaudeOpusModel, nil
		case "claude-sonnet-4-6", "claude-sonnet", "sonnet":
			return ClaudeSonnetModel, nil
		case "claude-haiku-4-6", "claude-haiku", "haiku":
			return ClaudeHaikuModel, nil
		default:
			return model, nil
		}
	}

	switch strings.ToLower(model) {
	case "gpt-5.3-codex", "codex":
		return CodexModel, nil
	case "gpt-5.3-codex-spark", "codex-spark", "spark":
		return CodexSparkModel, nil
	case "opus", "opusplan", "sonnet", "haiku":
		return "", fmt.Errorf(
			"model %q looks like a Claude selector and cannot be used with vendor=codex; use %q or %q, or switch this scope to native cleanup mode for direct Claude routing",
			model,
			CodexModel,
			CodexSparkModel,
		)
	default:
		if strings.HasPrefix(strings.ToLower(model), "claude-") {
			return "", fmt.Errorf(
				"model %q looks like a Claude selector and cannot be used with vendor=codex; use %q or %q, or switch this scope to native cleanup mode for direct Claude routing",
				model,
				CodexModel,
				CodexSparkModel,
			)
		}
		return model, nil
	}
}
