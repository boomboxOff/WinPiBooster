package main

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
)

// ─── Update.KB() ──────────────────────────────────────────────────────────────

func TestUpdateKB_String(t *testing.T) {
	u := Update{KBArticleIDs: "5034441"}
	if got := u.KB(); got != "5034441" {
		t.Errorf("KB() = %q, want %q", got, "5034441")
	}
}

func TestUpdateKB_Slice(t *testing.T) {
	u := Update{KBArticleIDs: []interface{}{"5034441", "5034442"}}
	got := u.KB()
	if got != "5034441, 5034442" {
		t.Errorf("KB() = %q, want %q", got, "5034441, 5034442")
	}
}

func TestUpdateKB_Nil(t *testing.T) {
	u := Update{KBArticleIDs: nil}
	got := u.KB()
	if got == "" {
		t.Error("KB() should not return empty string for nil")
	}
}

// ─── Update.Computer() ────────────────────────────────────────────────────────

func TestUpdateComputer_Empty(t *testing.T) {
	u := Update{PSComputerName: ""}
	if got := u.Computer(); got != "local" {
		t.Errorf("Computer() = %q, want %q", got, "local")
	}
}

func TestUpdateComputer_Named(t *testing.T) {
	u := Update{PSComputerName: "DESKTOP-PI"}
	if got := u.Computer(); got != "DESKTOP-PI" {
		t.Errorf("Computer() = %q, want %q", got, "DESKTOP-PI")
	}
}

// ─── writeStatusJSON() ────────────────────────────────────────────────────────

func TestWriteStatusJSON(t *testing.T) {
	oldDir := logDir
	logDir = t.TempDir()
	defer func() { logDir = oldDir }()

	atomic.StoreInt64(&updatesChecked, 10)
	atomic.StoreInt64(&updatesInstalled, 3)
	atomic.StoreInt64(&updatesSkipped, 6)
	atomic.StoreInt64(&cycleErrors, 1)
	defer func() {
		atomic.StoreInt64(&updatesChecked, 0)
		atomic.StoreInt64(&updatesInstalled, 0)
		atomic.StoreInt64(&updatesSkipped, 0)
		atomic.StoreInt64(&cycleErrors, 0)
	}()

	writeStatusJSON()

	data, err := os.ReadFile(filepath.Join(logDir, "status.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var s statusJSON
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if s.UpdatesChecked != 10 {
		t.Errorf("UpdatesChecked = %d, want 10", s.UpdatesChecked)
	}
	if s.UpdatesInstalled != 3 {
		t.Errorf("UpdatesInstalled = %d, want 3", s.UpdatesInstalled)
	}
	if s.CycleErrors != 1 {
		t.Errorf("CycleErrors = %d, want 1", s.CycleErrors)
	}
	if s.LastCheck == "" {
		t.Error("LastCheck should not be empty")
	}
}

// ─── uptime_seconds in status.json ────────────────────────────────────────────

func TestWriteStatusJSON_UptimeSeconds(t *testing.T) {
	oldDir := logDir
	logDir = t.TempDir()
	defer func() { logDir = oldDir }()

	startTime = time.Now().Add(-5 * time.Second)
	defer func() { startTime = time.Now() }()

	writeStatusJSON()

	data, err := os.ReadFile(filepath.Join(logDir, "status.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var s statusJSON
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if s.UptimeSeconds < 4 {
		t.Errorf("UptimeSeconds = %d, want >= 4", s.UptimeSeconds)
	}
}

// ─── writeStatusJSON ──────────────────────────────────────────────────────────

func TestWriteStatusJSON_WritesFile(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	atomic.StoreInt64(&updatesChecked, 7)
	atomic.StoreInt64(&updatesInstalled, 2)
	defer func() {
		atomic.StoreInt64(&updatesChecked, 0)
		atomic.StoreInt64(&updatesInstalled, 0)
	}()

	writeStatusJSON()

	data, err := os.ReadFile(filepath.Join(dir, "status.json"))
	if err != nil {
		t.Fatalf("status.json not written: %v", err)
	}
	var s statusJSON
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if s.UpdatesChecked != 7 {
		t.Errorf("updates_checked = %d, want 7", s.UpdatesChecked)
	}
	if s.UpdatesInstalled != 2 {
		t.Errorf("updates_installed = %d, want 2", s.UpdatesInstalled)
	}
}

// ─── parseRebootPending() ─────────────────────────────────────────────────────

func TestParseRebootPending(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"True", true},
		{"True\r\n", true},
		{"  True  ", true},
		{"False", false},
		{"False\r\n", false},
		{"", false},
		{"true", false}, // case-sensitive, PowerShell outputs "True"
	}
	for _, c := range cases {
		if got := parseRebootPending(c.input); got != c.want {
			t.Errorf("parseRebootPending(%q) = %v, want %v", c.input, got, c.want)
		}
	}
}

// ─── --version flag parsing ───────────────────────────────────────────────────

func TestVersionFlagDetection(t *testing.T) {
	args := []string{"prog", "--version"}
	found := false
	for _, arg := range args[1:] {
		if arg == "--version" {
			found = true
		}
	}
	if !found {
		t.Error("--version flag not detected")
	}
}

// ─── dry-run flag parsing ─────────────────────────────────────────────────────

func TestDryRunFlagDetection(t *testing.T) {
	cases := []struct {
		args    []string
		wantDry bool
		wantCmd string
	}{
		{[]string{"prog", "--dry-run"}, true, ""},
		{[]string{"prog"}, false, ""},
		{[]string{"prog", "status"}, false, "status"},
		{[]string{"prog", "--dry-run", "status"}, true, "status"},
	}
	for _, c := range cases {
		dryRun := false
		filtered := c.args[:1]
		for _, arg := range c.args[1:] {
			if arg == "--dry-run" {
				dryRun = true
			} else {
				filtered = append(filtered, arg)
			}
		}
		cmd := ""
		if len(filtered) > 1 {
			cmd = filtered[1]
		}
		if dryRun != c.wantDry {
			t.Errorf("args=%v: dryRun=%v, want %v", c.args, dryRun, c.wantDry)
		}
		if cmd != c.wantCmd {
			t.Errorf("args=%v: cmd=%q, want %q", c.args, cmd, c.wantCmd)
		}
	}
}

// ─── cleanOldLogsVerbose() ────────────────────────────────────────────────────

func TestCleanOldLogsVerbose_RemovesOldArchives(t *testing.T) {
	dir := t.TempDir()
	oldDir := logDir
	logDir = dir
	defer func() { logDir = oldDir }()

	// Create an old archive (40 days ago) and a recent one (1 day ago).
	old := filepath.Join(dir, "UpdateLog_old.txt")
	recent := filepath.Join(dir, "UpdateLog_recent.txt")
	oldTime := time.Now().AddDate(0, 0, -40)

	if err := os.WriteFile(old, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(old, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recent, []byte("recent"), 0644); err != nil {
		t.Fatal(err)
	}

	cleanOldLogsVerbose(false)

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("old archive should have been removed")
	}
	if _, err := os.Stat(recent); os.IsNotExist(err) {
		t.Error("recent archive should still exist")
	}
}

func TestCleanOldLogsVerbose_WithOutput(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// Create an expired archive
	oldFile := filepath.Join(dir, "UpdateLog_expired.txt")
	if err := os.WriteFile(oldFile, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	pastTime := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(oldFile, pastTime, pastTime); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	cleanOldLogsVerbose(true)
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "1 archive(s) supprimée(s)") {
		t.Errorf("expected verbose output with count, got: %q", out)
	}
}

func TestCleanOldLogsVerbose_RecentFileNotDeleted(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// Recent archive (not expired)
	recent := filepath.Join(dir, "UpdateLog_recent.txt")
	if err := os.WriteFile(recent, []byte("recent"), 0644); err != nil {
		t.Fatal(err)
	}

	cleanOldLogsVerbose(false)

	if _, err := os.Stat(recent); os.IsNotExist(err) {
		t.Error("recent log file should NOT be deleted")
	}
}

func TestCleanOldLogsVerbose_NonMatchingFile(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// File that doesn't match UpdateLog_*.txt pattern
	other := filepath.Join(dir, "otherfile.txt")
	if err := os.WriteFile(other, []byte("other"), 0644); err != nil {
		t.Fatal(err)
	}

	cleanOldLogsVerbose(false)

	if _, err := os.Stat(other); os.IsNotExist(err) {
		t.Error("non-matching file should NOT be deleted")
	}
}

func TestCleanOldLogsVerbose_WithLogger_DeletesExpired(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	oldFile := filepath.Join(dir, "UpdateLog_expired.txt")
	if err := os.WriteFile(oldFile, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	pastTime := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(oldFile, pastTime, pastTime); err != nil {
		t.Fatal(err)
	}

	withTestLogger(t, func() {
		cleanOldLogsVerbose(false) // log != nil → covers log.Debugf branch
	})

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("expired archive should have been removed")
	}
}

// ─── shutdownCtx ──────────────────────────────────────────────────────────────

func TestShutdownCtx_InitiallyNotDone(t *testing.T) {
	if shutdownCtx == nil {
		t.Fatal("shutdownCtx is nil")
	}
	if err := shutdownCtx.Err(); err != nil {
		t.Errorf("shutdownCtx.Err() = %v, want nil (context should not be cancelled at startup)", err)
	}
}

// ─── recordInstalled / last_installed history ─────────────────────────────────

func TestRecordInstalled_CapAt10(t *testing.T) {
	lastInstalledMu.Lock()
	lastInstalled = nil
	lastInstalledMu.Unlock()
	defer func() {
		lastInstalledMu.Lock()
		lastInstalled = nil
		lastInstalledMu.Unlock()
	}()

	for i := 0; i < 12; i++ {
		recordInstalled([]installEntry{{KB: "KB100", Title: "T", InstalledAt: "2026-01-01T00:00:00Z"}})
	}

	lastInstalledMu.Lock()
	n := len(lastInstalled)
	lastInstalledMu.Unlock()

	if n != 10 {
		t.Errorf("lastInstalled len = %d, want 10", n)
	}
}

func TestWriteStatusJSON_IncludesLastInstalled(t *testing.T) {
	oldDir := logDir
	logDir = t.TempDir()
	defer func() { logDir = oldDir }()

	lastInstalledMu.Lock()
	lastInstalled = []installEntry{{KB: "KB5034441", Title: "Security Update", InstalledAt: "2026-01-01T00:00:00Z"}}
	lastInstalledMu.Unlock()
	defer func() {
		lastInstalledMu.Lock()
		lastInstalled = nil
		lastInstalledMu.Unlock()
	}()

	writeStatusJSON()

	data, err := os.ReadFile(filepath.Join(logDir, "status.json"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var s statusJSON
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(s.LastInstalled) != 1 {
		t.Fatalf("LastInstalled len = %d, want 1", len(s.LastInstalled))
	}
	if s.LastInstalled[0].KB != "KB5034441" {
		t.Errorf("KB = %q, want KB5034441", s.LastInstalled[0].KB)
	}
}

// ─── install --start flag detection ───────────────────────────────────────────

func TestInstallStartFlag_Detected(t *testing.T) {
	args := []string{"WinPiBooster.exe", "install", "--start"}
	found := false
	for _, arg := range args[2:] {
		if arg == "--start" {
			found = true
			break
		}
	}
	if !found {
		t.Error("--start flag not detected in args")
	}
}

func TestInstallStartFlag_Absent(t *testing.T) {
	args := []string{"WinPiBooster.exe", "install"}
	found := false
	for _, arg := range args[2:] {
		if arg == "--start" {
			found = true
			break
		}
	}
	if found {
		t.Error("--start flag should not be detected when absent")
	}
}

// ─── withTestLogger helper ────────────────────────────────────────────────────

// withTestLogger temporarily sets the global log to a discard logger, then restores it.
func withTestLogger(t *testing.T, fn func()) {
	t.Helper()
	oldLog := log
	l := logrus.New()
	l.SetOutput(io.Discard)
	log = l
	defer func() { log = oldLog }()
	fn()
}

// ─── retryAttempts ────────────────────────────────────────────────────────────

func TestRetryAttempts_Default(t *testing.T) {
	old := cfg
	cfg = defaults()
	defer func() { cfg = old }()

	if got := retryAttempts(); got != 3 {
		t.Errorf("retryAttempts() = %d, want 3", got)
	}
}

func TestRetryAttempts_Custom(t *testing.T) {
	old := cfg
	cfg = defaults()
	cfg.RetryAttempts = 5
	defer func() { cfg = old }()

	if got := retryAttempts(); got != 5 {
		t.Errorf("retryAttempts() = %d, want 5", got)
	}
}

// ─── retryAttempts zero fallback ──────────────────────────────────────────────

func TestRetryAttempts_ZeroFallback(t *testing.T) {
	old := cfg
	cfg = defaults()
	cfg.RetryAttempts = 0
	defer func() { cfg = old }()

	if got := retryAttempts(); got != 3 {
		t.Errorf("retryAttempts() with 0 = %d, want 3 (fallback)", got)
	}
}

// ─── retryBackoff ─────────────────────────────────────────────────────────────

func TestRetryBackoff_SuccessOnFirst(t *testing.T) {
	withTestLogger(t, func() {
		called := 0
		err := retryBackoff("test", 3, []time.Duration{1 * time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond}, func() error {
			called++
			return nil
		})
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
		if called != 1 {
			t.Errorf("expected 1 call, got %d", called)
		}
	})
}

func TestRetryBackoff_FailsAllAttempts(t *testing.T) {
	withTestLogger(t, func() {
		called := 0
		testErr := errors.New("test error")
		err := retryBackoff("test", 2, []time.Duration{1 * time.Millisecond}, func() error {
			called++
			return testErr
		})
		if err != testErr {
			t.Errorf("expected testErr, got %v", err)
		}
		if called != 2 {
			t.Errorf("expected 2 calls, got %d", called)
		}
	})
}

func TestRetryBackoff_SuccessOnSecond(t *testing.T) {
	withTestLogger(t, func() {
		attempt := 0
		testErr := errors.New("fail once")
		err := retryBackoff("test", 3, []time.Duration{1 * time.Millisecond, 2 * time.Millisecond}, func() error {
			attempt++
			if attempt == 1 {
				return testErr
			}
			return nil
		})
		if err != nil {
			t.Errorf("expected nil on second attempt, got %v", err)
		}
	})
}

// ─── cleanOldLogs ─────────────────────────────────────────────────────────────

func TestCleanOldLogs_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()
	// Must not panic or return error
	cleanOldLogs()
}

func TestCleanOldLogs_DeletesExpired(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// Write an old archive (modification time set to 60 days ago)
	oldFile := filepath.Join(dir, "UpdateLog_old.txt")
	if err := os.WriteFile(oldFile, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	pastTime := time.Now().Add(-60 * 24 * time.Hour)
	if err := os.Chtimes(oldFile, pastTime, pastTime); err != nil {
		t.Fatal(err)
	}

	cleanOldLogs()

	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Error("expected old log file to be deleted")
	}
}

// ─── min() ────────────────────────────────────────────────────────────────────

func TestMin(t *testing.T) {
	cases := []struct{ a, b, want int }{
		{1, 2, 1},
		{2, 1, 1},
		{0, 0, 0},
		{-1, 1, -1},
	}
	for _, c := range cases {
		if got := min(c.a, c.b); got != c.want {
			t.Errorf("min(%d, %d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// ─── showNotification disabled ────────────────────────────────────────────────

func TestShowNotification_DisabledNoOp(t *testing.T) {
	old := cfg
	cfg = defaults()
	cfg.NotificationsEnabled = boolPtr(false)
	defer func() { cfg = old }()

	// Must be a no-op, no panic
	showNotification("Test", "Notification désactivée")
}

func TestShowNotification_WithLogger_FailsSilently(t *testing.T) {
	withTestLogger(t, func() {
		old := cfg
		cfg = defaults() // notifications enabled
		defer func() { cfg = old }()
		// toast.Push() will fail in test environment; log.Debugf must be called safely
		showNotification("Test", "Should fail silently with non-nil log")
	})
}

// ─── acquireSingleInstanceMutex() ────────────────────────────────────────────

func TestAcquireSingleInstanceMutex_FirstSucceeds(t *testing.T) {
	h, err := acquireSingleInstanceMutex()
	if err != nil {
		t.Fatalf("first acquire should succeed: %v", err)
	}
	windows.CloseHandle(h)
}

func TestAcquireSingleInstanceMutex_SecondFails(t *testing.T) {
	h, err := acquireSingleInstanceMutex()
	if err != nil {
		t.Fatalf("first acquire should succeed: %v", err)
	}
	defer windows.CloseHandle(h)

	_, err2 := acquireSingleInstanceMutex()
	if err2 == nil {
		t.Fatal("second acquire should fail when first is held")
	}
	if !strings.Contains(err2.Error(), "déjà en cours d'exécution") {
		t.Errorf("unexpected error message: %v", err2)
	}
}

// ─── deaccent (#103) ──────────────────────────────────────────────────────────

func TestDeaccent_NoAccents(t *testing.T) {
	in := "Hello World 123"
	if got := deaccent(in); got != in {
		t.Errorf("deaccent(%q) = %q, want %q", in, got, in)
	}
}

func TestDeaccent_LowerCase(t *testing.T) {
	cases := map[string]string{
		"à": "a", "â": "a",
		"è": "e", "é": "e", "ê": "e", "ë": "e",
		"î": "i", "ï": "i",
		"ô": "o",
		"ù": "u", "û": "u", "ü": "u",
		"ç": "c",
	}
	for in, want := range cases {
		if got := deaccent(in); got != want {
			t.Errorf("deaccent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeaccent_UpperCase(t *testing.T) {
	cases := map[string]string{
		"À": "A", "Â": "A",
		"È": "E", "É": "E", "Ê": "E", "Ë": "E",
		"Î": "I", "Ï": "I",
		"Ô": "O",
		"Ù": "U", "Û": "U", "Ü": "U",
		"Ç": "C",
	}
	for in, want := range cases {
		if got := deaccent(in); got != want {
			t.Errorf("deaccent(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeaccent_Ligatures(t *testing.T) {
	if got := deaccent("æ"); got != "ae" {
		t.Errorf("deaccent(æ) = %q, want %q", got, "ae")
	}
	if got := deaccent("Æ"); got != "AE" {
		t.Errorf("deaccent(Æ) = %q, want %q", got, "AE")
	}
	if got := deaccent("œ"); got != "oe" {
		t.Errorf("deaccent(œ) = %q, want %q", got, "oe")
	}
	if got := deaccent("Œ"); got != "OE" {
		t.Errorf("deaccent(Œ) = %q, want %q", got, "OE")
	}
}

func TestDeaccent_FullSentence(t *testing.T) {
	in := "Mises à jour Windows installées : KB5034441"
	want := "Mises a jour Windows installees : KB5034441"
	if got := deaccent(in); got != want {
		t.Errorf("deaccent(%q) = %q, want %q", in, got, want)
	}
}

func TestDeaccent_Empty(t *testing.T) {
	if got := deaccent(""); got != "" {
		t.Errorf("deaccent(\"\") = %q, want \"\"", got)
	}
}
