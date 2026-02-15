package builtin

import (
	"path/filepath"
	"testing"
)

func TestWriteLoadConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "builtin-config.json")
	in := ServeConfig{
		Listen:    "127.0.0.1:18317",
		Model:     "test-model",
		BackendID: "builtin",
	}
	if err := WriteConfig(p, in); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	out, err := LoadConfig(p)
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	if out.Listen != in.Listen || out.Model != in.Model || out.BackendID != in.BackendID {
		t.Fatalf("unexpected config: %+v", out)
	}
}

