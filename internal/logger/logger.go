package logger

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// dailyWriter creates a new log file each calendar day under dir.
// On rotation it asynchronously deletes files older than retentionDays.
// retentionDays <= 0 disables deletion.
type dailyWriter struct {
	mu            sync.Mutex
	dir           string
	retentionDays int
	day           string
	f             *os.File
}

func (w *dailyWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	today := time.Now().Local().Format("2006-01-02")
	if w.f == nil || today != w.day {
		if w.f != nil {
			_ = w.f.Close()
		}
		w.day = today
		path := filepath.Join(w.dir, "secretweave-"+today+".log")
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return 0, err
		}
		w.f = f
		if w.retentionDays > 0 {
			go w.cleanup()
		}
	}
	return w.f.Write(p)
}

// cleanup removes log files older than retentionDays. Safe to call concurrently
// with Write because it only touches files from previous days.
func (w *dailyWriter) cleanup() {
	cutoff := time.Now().Local().AddDate(0, 0, -w.retentionDays)
	matches, err := filepath.Glob(filepath.Join(w.dir, "secretweave-*.log"))
	if err != nil {
		return
	}
	for _, path := range matches {
		base := filepath.Base(path)
		dateStr := strings.TrimSuffix(strings.TrimPrefix(base, "secretweave-"), ".log")
		t, err := time.ParseInLocation("2006-01-02", dateStr, time.Local)
		if err != nil {
			continue
		}
		if t.Before(cutoff) {
			_ = os.Remove(path)
		}
	}
}

// New builds a JSON structured logger that writes to both stdout and daily
// rotated files under logDir. Files older than retentionDays are removed
// automatically; pass 0 to disable retention enforcement.
func New(logDir string, retentionDays int) *slog.Logger {
	if err := os.MkdirAll(logDir, 0755); err != nil {
		slog.Default().Error("failed to create log directory", "dir", logDir, "err", err)
	}

	fw := &dailyWriter{dir: logDir, retentionDays: retentionDays}
	if retentionDays > 0 {
		fw.cleanup() // purge stale files from previous runs at startup
	}

	w := io.MultiWriter(os.Stdout, fw)
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
