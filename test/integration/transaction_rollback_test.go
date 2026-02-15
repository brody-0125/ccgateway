package integration

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	installtx "ccgateway/internal/install"
)

func TestTransactionRollback(t *testing.T) {
	dir := t.TempDir()
	mark := filepath.Join(dir, "mark.txt")
	tx := installtx.New(nil)
	tx.Add("write-mark", func() error {
		return os.WriteFile(mark, []byte("ok"), 0o644)
	}, func() error {
		return os.Remove(mark)
	})
	tx.Add("fail", func() error {
		return errors.New("boom")
	}, nil)

	if err := tx.Run(); err == nil {
		t.Fatal("expected transaction failure")
	}
	if _, err := os.Stat(mark); !os.IsNotExist(err) {
		t.Fatalf("expected rollback to remove file, stat err=%v", err)
	}
}

func TestTransactionRollbackError(t *testing.T) {
	tx := installtx.New(nil)
	tx.Add("first", func() error { return nil }, func() error { return errors.New("undo failed") })
	tx.Add("second", func() error { return errors.New("do failed") }, nil)
	err := tx.Run()
	if err == nil {
		t.Fatal("expected error")
	}
	var rb *installtx.RollbackError
	if !errors.As(err, &rb) {
		t.Fatalf("expected RollbackError, got %T", err)
	}
}
