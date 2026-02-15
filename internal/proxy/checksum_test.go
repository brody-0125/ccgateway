package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseChecksums(t *testing.T) {
	input := "abc123  bad\n"
	if _, err := ParseChecksums(input); err == nil {
		t.Fatalf("expected error for invalid hash")
	}

	input = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  file.tar.gz\n"
	m, err := ParseChecksums(input)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if m["file.tar.gz"] != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("unexpected checksum map: %#v", m)
	}
}

func TestVerifyFileChecksum(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.txt")
	if err := os.WriteFile(p, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	ok, actual, err := VerifyFileChecksum(p, "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824")
	if err != nil {
		t.Fatalf("verify failed: %v", err)
	}
	if !ok {
		t.Fatalf("expected checksum match, actual=%s", actual)
	}
}
