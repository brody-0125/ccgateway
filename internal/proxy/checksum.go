package proxy

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
)

func ParseChecksums(content string) (map[string]string, error) {
	result := map[string]string{}
	s := bufio.NewScanner(strings.NewReader(content))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return nil, fmt.Errorf("invalid checksum line: %q", line)
		}
		hash := strings.ToLower(strings.TrimSpace(parts[0]))
		name := strings.TrimSpace(parts[len(parts)-1])
		name = strings.TrimPrefix(name, "*")
		if len(hash) != 64 {
			return nil, fmt.Errorf("invalid sha256 hash: %q", hash)
		}
		result[name] = hash
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no checksums found")
	}
	return result, nil
}

func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func VerifyFileChecksum(path, expected string) (bool, string, error) {
	actual, err := SHA256File(path)
	if err != nil {
		return false, "", err
	}
	return strings.EqualFold(actual, expected), actual, nil
}
