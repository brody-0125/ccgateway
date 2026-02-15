package logx

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Logger struct {
	path string
	mu   sync.Mutex
}

func New(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	_ = f.Close()
	return &Logger{path: path}, nil
}

func (l *Logger) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

func (l *Logger) Infof(format string, args ...any) {
	if l == nil {
		return
	}
	l.write("INFO", fmt.Sprintf(format, args...))
}

func (l *Logger) Errorf(code string, err error, format string, args ...any) {
	if l == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	if err != nil {
		msg = fmt.Sprintf("%s | err=%v", msg, err)
	}
	if code != "" {
		msg = fmt.Sprintf("code=%s | %s", code, msg)
	}
	l.write("ERROR", msg)
}

func (l *Logger) write(level, message string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = fmt.Fprintf(f, "%s [%s] %s\n", time.Now().UTC().Format(time.RFC3339), level, message)
}
