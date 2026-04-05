package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type DailyFileWriter struct {
	dir      string
	mu       sync.Mutex
	date     string
	file     *os.File
	fallback io.Writer
}

func NewDailyFileWriter(dir string, fallback io.Writer) (*DailyFileWriter, error) {
	if fallback == nil {
		fallback = os.Stdout
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	w := &DailyFileWriter{dir: dir, fallback: fallback}
	if err := w.rotateIfNeeded(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *DailyFileWriter) rotateIfNeeded() error {
	today := time.Now().UTC().Format("2006-01-02")
	if w.file != nil && w.date == today {
		return nil
	}
	if w.file != nil {
		_ = w.file.Close()
	}
	filename := filepath.Join(w.dir, today+".log")
	f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log file: %w", err)
	}
	w.file = f
	w.date = today
	return nil
}

func (w *DailyFileWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.rotateIfNeeded(); err != nil {
		_, _ = w.fallback.Write([]byte("[logger] " + err.Error() + "\n"))
		return w.fallback.Write(p)
	}
	n, err := w.file.Write(p)
	if err == nil {
		_, _ = w.fallback.Write(p)
	}
	return n, err
}

func New(dir string) (*log.Logger, error) {
	w, err := NewDailyFileWriter(dir, os.Stdout)
	if err != nil {
		return nil, err
	}
	return log.New(w, "", log.LstdFlags|log.Lmicroseconds|log.LUTC), nil
}
