package control

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"ccgateway/internal/netutil"
)

type ActivePointer struct {
	SchemaVersion    int    `json:"schema_version"`
	ActiveVendor     string `json:"active_vendor"`
	ActiveProfile    string `json:"active_profile"`
	ActiveGeneration string `json:"active_generation"`
	UpdatedAt        string `json:"updated_at"`
}

type PortRegistry struct {
	SchemaVersion int            `json:"schema_version"`
	Scopes        map[string]int `json:"scopes"`
	UpdatedAt     string         `json:"updated_at"`
}

var lockMu sync.Mutex

func LoadActive(path string) (ActivePointer, error) {
	out := ActivePointer{SchemaVersion: 1}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	if len(b) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out, err
	}
	if out.SchemaVersion == 0 {
		out.SchemaVersion = 1
	}
	return out, nil
}

func SaveActive(path string, active ActivePointer) error {
	active.SchemaVersion = 1
	active.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	b, err := json.MarshalIndent(active, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeAtomic(path, b, 0o644)
}

func WithSwitchLock(lockPath string, fn func() error) error {
	if fn == nil {
		return nil
	}
	lockMu.Lock()
	defer lockMu.Unlock()

	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	}()
	return fn()
}

func NextGeneration() string {
	return fmt.Sprintf("gen-%d", time.Now().UTC().UnixNano())
}

func LoadPorts(path string) (PortRegistry, error) {
	out := PortRegistry{SchemaVersion: 1, Scopes: map[string]int{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return out, nil
		}
		return out, err
	}
	if len(b) == 0 {
		return out, nil
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out, err
	}
	if out.SchemaVersion == 0 {
		out.SchemaVersion = 1
	}
	if out.Scopes == nil {
		out.Scopes = map[string]int{}
	}
	return out, nil
}

func SavePorts(path string, p PortRegistry) error {
	p.SchemaVersion = 1
	if p.Scopes == nil {
		p.Scopes = map[string]int{}
	}
	p.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return writeAtomic(path, b, 0o644)
}

func AllocateScopePort(regPath, scopeID string, preferred int, isHealthy func(int) bool) (int, error) {
	reg, err := LoadPorts(regPath)
	if err != nil {
		return 0, err
	}
	if p, ok := reg.Scopes[scopeID]; ok && p > 0 && p <= 65535 {
		if isHealthy != nil && isHealthy(p) {
			return p, nil
		}
		if netutil.IsLocalPortFree(p) {
			return p, nil
		}
	}
	port, err := netutil.FindAvailablePort(preferred, 100, isHealthy)
	if err != nil {
		return 0, err
	}
	reg.Scopes[scopeID] = port
	if err := SavePorts(regPath, reg); err != nil {
		return 0, err
	}
	return port, nil
}

func ReleaseScopePort(regPath, scopeID string) error {
	reg, err := LoadPorts(regPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	delete(reg.Scopes, scopeID)
	return SavePorts(regPath, reg)
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
