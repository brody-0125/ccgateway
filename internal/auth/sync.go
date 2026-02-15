package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	cberr "ccgateway/internal/errors"
)

type codexAuth struct {
	Tokens struct {
		AccessToken  string `json:"access_token"`
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
		AccountID    string `json:"account_id"`
	} `json:"tokens"`
	LastRefresh string `json:"last_refresh"`
}

type bridgeAuth struct {
	AccessToken  string `json:"access_token"`
	AccountID    string `json:"account_id"`
	Disabled     bool   `json:"disabled"`
	Email        string `json:"email"`
	Expired      string `json:"expired"`
	IDToken      string `json:"id_token"`
	LastRefresh  string `json:"last_refresh"`
	RefreshToken string `json:"refresh_token"`
	Type         string `json:"type"`
}

func Sync(sourcePath, targetPath string) error {
	b, err := os.ReadFile(sourcePath)
	if err != nil {
		return cberr.Wrap(cberr.ErrAuthSyncFailed, "failed to read auth source", err)
	}
	var src codexAuth
	if err := json.Unmarshal(b, &src); err != nil {
		return cberr.Wrap(cberr.ErrAuthSyncFailed, "failed to parse auth source", err)
	}
	if src.Tokens.AccessToken == "" {
		return cberr.New(cberr.ErrAuthSyncFailed, "missing tokens.access_token")
	}
	if src.LastRefresh == "" {
		src.LastRefresh = fmt.Sprintf("%d", time.Now().UTC().Unix())
	}

	dst := bridgeAuth{
		AccessToken:  src.Tokens.AccessToken,
		AccountID:    src.Tokens.AccountID,
		Disabled:     false,
		Email:        "",
		Expired:      "",
		IDToken:      src.Tokens.IDToken,
		LastRefresh:  src.LastRefresh,
		RefreshToken: src.Tokens.RefreshToken,
		Type:         "codex",
	}
	out, err := json.MarshalIndent(dst, "", "  ")
	if err != nil {
		return cberr.Wrap(cberr.ErrAuthSyncFailed, "failed to encode bridge auth", err)
	}
	out = append(out, '\n')

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
		return cberr.Wrap(cberr.ErrAuthSyncFailed, "failed to create auth target directory", err)
	}
	if err := writeAtomic(targetPath, out, 0o600); err != nil {
		return cberr.Wrap(cberr.ErrAuthSyncFailed, "failed to write auth target", err)
	}
	return nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
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
