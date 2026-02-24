package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// ─── levelUpper() ─────────────────────────────────────────────────────────────

func TestLevelUpper(t *testing.T) {
	cases := []struct{ in, want string }{
		{"info", "INFO"},
		{"error", "ERROR"},
		{"warning", "WARNING"},
		{"debug", "DEBUG"},
		{"INFO", "INFO"},
		{"", ""},
	}
	for _, c := range cases {
		if got := levelUpper(c.in); got != c.want {
			t.Errorf("levelUpper(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ─── fileHook size rotation ───────────────────────────────────────────────────

func TestFileHook_SizeRotation(t *testing.T) {
	tmp, err := os.CreateTemp("", "testlog*.txt")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(tmp.Name())

	rotated := make(chan struct{}, 1)
	h := &fileHook{
		file:         tmp,
		logPath:      tmp.Name(),
		levels:       []logrus.Level{logrus.InfoLevel},
		maxSizeBytes: 50, // tiny limit
		rotateFn: func() {
			select {
			case rotated <- struct{}{}:
			default:
			}
		},
	}

	entry := &logrus.Entry{
		Logger:  logrus.New(),
		Level:   logrus.InfoLevel,
		Time:    time.Now(),
		Message: strings.Repeat("x", 60), // well above the 50-byte limit
	}

	if err := h.Fire(entry); err != nil {
		t.Fatalf("Fire: %v", err)
	}

	select {
	case <-rotated:
		// expected
	case <-time.After(time.Second):
		t.Error("rotateFn was not called within 1 second")
	}
}

func TestFileHook_NoRotationBelowLimit(t *testing.T) {
	tmp, err := os.CreateTemp("", "testlog*.txt")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	defer os.Remove(tmp.Name())

	called := false
	h := &fileHook{
		file:         tmp,
		logPath:      tmp.Name(),
		levels:       []logrus.Level{logrus.InfoLevel},
		maxSizeBytes: 10 * 1024 * 1024, // 10 MB — way above what we'll write
		rotateFn:     func() { called = true },
	}

	entry := &logrus.Entry{
		Logger:  logrus.New(),
		Level:   logrus.InfoLevel,
		Time:    time.Now(),
		Message: "short",
	}

	if err := h.Fire(entry); err != nil {
		t.Fatalf("Fire: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // let any goroutine run
	if called {
		t.Error("rotateFn should not have been called for small log")
	}
}

// ─── newFileHook / fileHook.Levels / ReopenFile / Close ──────────────────────

func TestNewFileHook_InvalidPath(t *testing.T) {
	_, err := newFileHook("/nonexistent/path/that/cannot/be/created.log", logrus.AllLevels)
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestFileHook_ReopenFile_Error(t *testing.T) {
	dir := t.TempDir()
	h, err := newFileHook(filepath.Join(dir, "test.log"), logrus.AllLevels)
	if err != nil {
		t.Fatalf("newFileHook: %v", err)
	}
	h.Close()
	h.logPath = "/nonexistent/path/cannot-reopen.log"
	if err := h.ReopenFile(); err == nil {
		t.Error("expected error for invalid reopen path")
	}
}

func TestNewFileHook_Creates(t *testing.T) {
	dir := t.TempDir()
	h, err := newFileHook(filepath.Join(dir, "test.log"), logrus.AllLevels)
	if err != nil {
		t.Fatalf("newFileHook: %v", err)
	}
	defer h.Close()

	levels := h.Levels()
	if len(levels) != len(logrus.AllLevels) {
		t.Errorf("Levels() len=%d, want %d", len(levels), len(logrus.AllLevels))
	}
}

func TestFileHook_ReopenFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "test.log")
	h, err := newFileHook(p, logrus.AllLevels)
	if err != nil {
		t.Fatalf("newFileHook: %v", err)
	}
	defer h.Close()

	if err := h.ReopenFile(); err != nil {
		t.Fatalf("ReopenFile: %v", err)
	}
}

func TestFileHook_Close(t *testing.T) {
	dir := t.TempDir()
	h, err := newFileHook(filepath.Join(dir, "test.log"), logrus.AllLevels)
	if err != nil {
		t.Fatalf("newFileHook: %v", err)
	}
	h.Close() // must not panic
}

// ─── coloredConsoleFormatter.Format ──────────────────────────────────────────

func TestColoredConsoleFormatter_Format_TraceLevel(t *testing.T) {
	// TraceLevel hits the default case in the color switch
	f := &coloredConsoleFormatter{}
	entry := &logrus.Entry{
		Logger:  logrus.New(),
		Level:   logrus.TraceLevel,
		Time:    time.Now(),
		Message: "trace msg",
	}
	b, err := f.Format(entry)
	if err != nil {
		t.Fatalf("Format(TraceLevel): %v", err)
	}
	if !strings.Contains(string(b), "trace msg") {
		t.Errorf("expected message in output, got: %q", string(b))
	}
}

func TestColoredConsoleFormatter_Format_AllLevels(t *testing.T) {
	f := &coloredConsoleFormatter{}
	levels := []logrus.Level{
		logrus.ErrorLevel,
		logrus.WarnLevel,
		logrus.InfoLevel,
		logrus.DebugLevel,
		logrus.FatalLevel,
		logrus.PanicLevel,
	}
	for _, lvl := range levels {
		entry := &logrus.Entry{
			Logger:  logrus.New(),
			Level:   lvl,
			Time:    time.Now(),
			Message: "test msg",
		}
		b, err := f.Format(entry)
		if err != nil {
			t.Errorf("Format(%v): %v", lvl, err)
		}
		if !strings.Contains(string(b), "test msg") {
			t.Errorf("Format(%v) missing message: %q", lvl, string(b))
		}
	}
}

// ─── consoleHook.Levels / Fire ────────────────────────────────────────────────

func TestConsoleHook_Levels(t *testing.T) {
	h := &consoleHook{
		writer:    io.Discard,
		formatter: &coloredConsoleFormatter{},
		levels:    logrus.AllLevels,
	}
	if len(h.Levels()) != len(logrus.AllLevels) {
		t.Errorf("Levels() len=%d, want %d", len(h.Levels()), len(logrus.AllLevels))
	}
}

func TestConsoleHook_Fire(t *testing.T) {
	h := &consoleHook{
		writer:    io.Discard,
		formatter: &coloredConsoleFormatter{},
		levels:    logrus.AllLevels,
	}
	entry := &logrus.Entry{
		Logger:  logrus.New(),
		Level:   logrus.InfoLevel,
		Time:    time.Now(),
		Message: "console test",
	}
	if err := h.Fire(entry); err != nil {
		t.Fatalf("Fire: %v", err)
	}
}
