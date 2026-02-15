package state

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type Scope struct {
	VendorID  string `json:"vendor_id"`
	ProfileID string `json:"profile_id"`
}

type State struct {
	SchemaVersion int    `json:"schema_version"`
	Version       string `json:"version,omitempty"`
	InstallID     string `json:"install_id"`

	Scope       Scope  `json:"scope"`
	RuntimeMode string `json:"runtime_mode,omitempty"`

	Proxy     ProxyState   `json:"proxy"`
	Backend   BackendState `json:"backend"`
	Service   ServiceState `json:"service"`
	Claude    ClaudeState  `json:"claude"`
	LastStep  string       `json:"last_step"`
	UpdatedAt string       `json:"updated_at"`
}

type ProxyState struct {
	Version    string `json:"version"`
	BinaryPath string `json:"binary_path"`
	SHA256     string `json:"sha256"`
}

type BackendState struct {
	ID         string `json:"id"`
	Version    string `json:"version"`
	BinaryPath string `json:"binary_path"`
	SHA256     string `json:"sha256"`
}

type ServiceState struct {
	ProxyLabel string `json:"proxy_label"`
	SyncLabel  string `json:"sync_label"`
	Running    bool   `json:"running,omitempty"`
}

type ClaudeState struct {
	Applied           bool   `json:"applied"`
	SnapshotPath      string `json:"snapshot_path"`
	SnapshotSHA256    string `json:"snapshot_sha256"`
	AppliedGeneration string `json:"applied_generation,omitempty"`
}

func Default() State {
	return State{
		SchemaVersion: 2,
		Version:       "0.1.0",
		InstallID:     fmt.Sprintf("install-%d", time.Now().UTC().UnixNano()),
		Scope: Scope{
			VendorID:  "codex",
			ProfileID: "default",
		},
		RuntimeMode: "gateway",
		Backend: BackendState{
			ID: "cliproxyapi",
		},
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

func Load(path string) (State, error) {
	st := Default()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return st, nil
		}
		return st, err
	}
	if len(b) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(b, &st); err != nil {
		return st, err
	}
	if st.SchemaVersion == 0 {
		st.SchemaVersion = 2
	}
	if st.Version == "" {
		st.Version = "0.1.0"
	}
	if st.InstallID == "" {
		st.InstallID = fmt.Sprintf("install-%d", time.Now().UTC().UnixNano())
	}
	if st.Scope.VendorID == "" {
		st.Scope.VendorID = "codex"
	}
	if st.Scope.ProfileID == "" {
		st.Scope.ProfileID = "default"
	}
	if st.RuntimeMode == "" {
		st.RuntimeMode = "gateway"
	}
	if st.Backend.ID == "" {
		st.Backend.ID = "cliproxyapi"
	}
	if st.Backend.Version == "" {
		st.Backend.Version = st.Proxy.Version
	}
	if st.Backend.BinaryPath == "" {
		st.Backend.BinaryPath = st.Proxy.BinaryPath
	}
	if st.Backend.SHA256 == "" {
		st.Backend.SHA256 = st.Proxy.SHA256
	}
	if st.Proxy.Version == "" {
		st.Proxy.Version = st.Backend.Version
	}
	if st.Proxy.BinaryPath == "" {
		st.Proxy.BinaryPath = st.Backend.BinaryPath
	}
	if st.Proxy.SHA256 == "" {
		st.Proxy.SHA256 = st.Backend.SHA256
	}
	return st, nil
}

func Save(path string, st State) error {
	if st.SchemaVersion == 0 {
		st.SchemaVersion = 2
	}
	if st.Version == "" {
		st.Version = "0.1.0"
	}
	if st.Backend.ID == "" {
		st.Backend.ID = "cliproxyapi"
	}
	if st.Backend.Version == "" {
		st.Backend.Version = st.Proxy.Version
	}
	if st.Backend.BinaryPath == "" {
		st.Backend.BinaryPath = st.Proxy.BinaryPath
	}
	if st.Backend.SHA256 == "" {
		st.Backend.SHA256 = st.Proxy.SHA256
	}
	if st.Proxy.Version == "" {
		st.Proxy.Version = st.Backend.Version
	}
	if st.Proxy.BinaryPath == "" {
		st.Proxy.BinaryPath = st.Backend.BinaryPath
	}
	if st.Proxy.SHA256 == "" {
		st.Proxy.SHA256 = st.Backend.SHA256
	}
	st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeAtomic(path, b, 0o644)
}

func UpdateStep(path string, st *State, step string) error {
	if st == nil {
		return fmt.Errorf("nil state")
	}
	st.LastStep = step
	return Save(path, *st)
}

func HashFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := fmt.Sprintf("%s.tmp.%d.%d", path, os.Getpid(), time.Now().UTC().UnixNano())
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
