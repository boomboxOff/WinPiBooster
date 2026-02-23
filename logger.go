package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mattn/go-colorable"
	"github.com/sirupsen/logrus"
)

// ANSI color codes for console output
const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorYellow = "\033[33m"
	colorGreen  = "\033[32m"
	colorCyan   = "\033[36m"
	colorWhite  = "\033[37m"
)

// coloredConsoleFormatter formats log entries with colors for the console
type coloredConsoleFormatter struct{}

func (f *coloredConsoleFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	ts := entry.Time.Format("15:04:05")
	level := entry.Level.String()
	msg := entry.Message

	var color string
	switch entry.Level {
	case logrus.ErrorLevel, logrus.FatalLevel, logrus.PanicLevel:
		color = colorRed
	case logrus.WarnLevel:
		color = colorYellow
	case logrus.InfoLevel:
		color = colorGreen
	case logrus.DebugLevel:
		color = colorCyan
	default:
		color = colorWhite
	}

	line := fmt.Sprintf("%s %s%s%s: %s\n", ts, color, level, colorReset, msg)
	return []byte(line), nil
}

// plainFileFormatter formats log entries as plain text for the file
type plainFileFormatter struct{}

func (f *plainFileFormatter) Format(entry *logrus.Entry) ([]byte, error) {
	ts := entry.Time.Format("2006-01-02 15:04:05")
	level := entry.Level.String()
	line := fmt.Sprintf("%s [%s]: %s\n", ts, levelUpper(level), entry.Message)
	return []byte(line), nil
}

func levelUpper(s string) string {
	upper := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' {
			c -= 32
		}
		upper[i] = c
	}
	return string(upper)
}

// fileHook is a logrus hook that writes plain-text log entries to UpdateLog.txt
type fileHook struct {
	mu       sync.Mutex
	file     *os.File
	logPath  string
	levels   []logrus.Level
}

func newFileHook(logPath string, levels []logrus.Level) (*fileHook, error) {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, err
	}
	return &fileHook{file: f, logPath: logPath, levels: levels}, nil
}

func (h *fileHook) Levels() []logrus.Level {
	return h.levels
}

func (h *fileHook) Fire(entry *logrus.Entry) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	formatter := &plainFileFormatter{}
	line, err := formatter.Format(entry)
	if err != nil {
		return err
	}
	_, err = h.file.Write(line)
	return err
}

// ReopenFile closes the current log file and reopens it (used after archiving).
func (h *fileHook) ReopenFile() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.file != nil {
		h.file.Close()
	}
	f, err := os.OpenFile(h.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}
	h.file = f
	return nil
}

// Close closes the underlying file.
func (h *fileHook) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.file != nil {
		h.file.Close()
	}
}

// setupLogger initialises and returns a logrus.Logger writing to both
// UpdateLog.txt (plain) and the console (coloured).
func setupLogger() (*logrus.Logger, *fileHook, error) {
	exePath, err := os.Executable()
	if err != nil {
		return nil, nil, fmt.Errorf("cannot find executable path: %w", err)
	}
	dir := filepath.Dir(exePath)
	logPath := filepath.Join(dir, "UpdateLog.txt")

	// Determine log level
	level := logrus.InfoLevel
	if os.Getenv("DEBUG") == "true" {
		level = logrus.DebugLevel
	}

	allLevels := []logrus.Level{
		logrus.PanicLevel,
		logrus.FatalLevel,
		logrus.ErrorLevel,
		logrus.WarnLevel,
		logrus.InfoLevel,
		logrus.DebugLevel,
	}

	hook, err := newFileHook(logPath, allLevels)
	if err != nil {
		return nil, nil, fmt.Errorf("cannot open log file %s: %w", logPath, err)
	}

	log := logrus.New()
	log.SetLevel(level)

	// Console output via go-colorable (handles Windows ANSI)
	colorableStdout := colorable.NewColorableStdout()
	log.SetOutput(io.Discard) // silence default output; we use hooks + console writer
	log.AddHook(hook)

	// Console writer hook
	log.AddHook(&consoleHook{
		writer:    colorableStdout,
		formatter: &coloredConsoleFormatter{},
		levels:    allLevels,
	})

	// Suppress default output
	log.SetFormatter(&logrus.TextFormatter{DisableColors: true})

	_ = time.Now() // keep time import used
	return log, hook, nil
}

// consoleHook writes coloured log lines to an io.Writer (stdout).
type consoleHook struct {
	writer    io.Writer
	formatter logrus.Formatter
	levels    []logrus.Level
}

func (h *consoleHook) Levels() []logrus.Level { return h.levels }

func (h *consoleHook) Fire(entry *logrus.Entry) error {
	line, err := h.formatter.Format(entry)
	if err != nil {
		return err
	}
	_, err = h.writer.Write(line)
	return err
}
