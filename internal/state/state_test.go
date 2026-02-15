package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateSaveLoadAndHash(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "state.json")

	st := Default()
	st.LastStep = "bootstrap"
	if err := Save(p, st); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	loaded, err := Load(p)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.LastStep != "bootstrap" {
		t.Fatalf("unexpected LastStep: %s", loaded.LastStep)
	}
	if loaded.Backend.ID != "cliproxyapi" {
		t.Fatalf("unexpected backend id: %q", loaded.Backend.ID)
	}
	h1, err := HashFile(p)
	if err != nil {
		t.Fatalf("hash failed: %v", err)
	}
	if h1 == "" {
		t.Fatal("expected non-empty hash")
	}
	if _, err := os.Stat(p + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("unexpected tmp file left behind")
	}
}
