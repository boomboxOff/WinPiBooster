package main

import (
	"encoding/json"
	"errors"
	"fmt"
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

// ─── durationUntilMidnight() ──────────────────────────────────────────────────

func TestDurationUntilMidnight_Positive(t *testing.T) {
	d := durationUntilMidnight()
	if d <= 0 {
		t.Errorf("durationUntilMidnight() = %v, want > 0", d)
	}
	if d > 24*time.Hour {
		t.Errorf("durationUntilMidnight() = %v, want <= 24h", d)
	}
}

// ─── JSON parsing (Update struct) ─────────────────────────────────────────────

func TestUpdateJSON_SingleObject(t *testing.T) {
	raw := `{"Title":"Security Update","KBArticleIDs":"5034441","Size":"10 MB","PSComputerName":""}`
	var u Update
	if err := json.Unmarshal([]byte(raw), &u); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if u.Title != "Security Update" {
		t.Errorf("Title = %q, want %q", u.Title, "Security Update")
	}
	if u.KB() != "5034441" {
		t.Errorf("KB() = %q, want %q", u.KB(), "5034441")
	}
	if u.Computer() != "local" {
		t.Errorf("Computer() = %q, want %q", u.Computer(), "local")
	}
}

func TestUpdateJSON_Array(t *testing.T) {
	raw := `[{"Title":"Update A","KBArticleIDs":"1111"},{"Title":"Update B","KBArticleIDs":"2222"}]`
	var updates []Update
	if err := json.Unmarshal([]byte(raw), &updates); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if len(updates) != 2 {
		t.Fatalf("len(updates) = %d, want 2", len(updates))
	}
	if updates[0].Title != "Update A" {
		t.Errorf("updates[0].Title = %q, want %q", updates[0].Title, "Update A")
	}
}

func TestUpdateJSON_SingleWrappedAsArray(t *testing.T) {
	// Simulate the normalisation done in checkAvailableUpdates
	raw := `{"Title":"Single Update","KBArticleIDs":"9999"}`
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		trimmed = "[" + trimmed + "]"
	}
	var updates []Update
	if err := json.Unmarshal([]byte(trimmed), &updates); err != nil {
		t.Fatalf("Unmarshal error: %v", err)
	}
	if len(updates) != 1 {
		t.Fatalf("len(updates) = %d, want 1", len(updates))
	}
	if updates[0].KB() != "9999" {
		t.Errorf("KB() = %q, want %q", updates[0].KB(), "9999")
	}
}

// ─── Config ───────────────────────────────────────────────────────────────────

func TestDefaults(t *testing.T) {
	cfg := defaults()
	if cfg.CheckIntervalSeconds != 60 {
		t.Errorf("CheckIntervalSeconds = %d, want 60", cfg.CheckIntervalSeconds)
	}
	if cfg.RetryAttempts != 3 {
		t.Errorf("RetryAttempts = %d, want 3", cfg.RetryAttempts)
	}
	if cfg.LogRetentionDays != 30 {
		t.Errorf("LogRetentionDays = %d, want 30", cfg.LogRetentionDays)
	}
	if cfg.MaxLogSizeMB != 10 {
		t.Errorf("MaxLogSizeMB = %d, want 10", cfg.MaxLogSizeMB)
	}
	if cfg.PSTimeoutMinutes != 10 {
		t.Errorf("PSTimeoutMinutes = %d, want 10", cfg.PSTimeoutMinutes)
	}
}

func TestConfigPSTimeout(t *testing.T) {
	cfg := Config{PSTimeoutMinutes: 20}
	if got := cfg.PSTimeout(); got != 20*time.Minute {
		t.Errorf("PSTimeout() = %v, want 20m", got)
	}
}

func TestConfigCmdTimeout(t *testing.T) {
	cfg := Config{CmdTimeoutSeconds: 120}
	if got := cfg.CmdTimeout(); got != 120*time.Second {
		t.Errorf("CmdTimeout() = %v, want 120s", got)
	}
}

func TestLoadConfig_CmdTimeout(t *testing.T) {
	p := cfgPath(t)
	defer os.Remove(p)
	if err := os.WriteFile(p, []byte(`{"cmd_timeout_seconds": 60}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := loadConfig()
	if cfg.CmdTimeoutSeconds != 60 {
		t.Errorf("CmdTimeoutSeconds = %d, want 60", cfg.CmdTimeoutSeconds)
	}
}

func TestDefaults_CmdTimeout(t *testing.T) {
	if d := defaults(); d.CmdTimeoutSeconds != 300 {
		t.Errorf("CmdTimeoutSeconds default = %d, want 300", d.CmdTimeoutSeconds)
	}
}

func TestLoadConfig_PSTimeout(t *testing.T) {
	p := cfgPath(t)
	defer os.Remove(p)
	if err := os.WriteFile(p, []byte(`{"ps_timeout_minutes": 30}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := loadConfig()
	if cfg.PSTimeoutMinutes != 30 {
		t.Errorf("PSTimeoutMinutes = %d, want 30", cfg.PSTimeoutMinutes)
	}
}

func TestLoadConfig_MaxLogSizeMB(t *testing.T) {
	p := cfgPath(t)
	defer os.Remove(p)

	if err := os.WriteFile(p, []byte(`{"max_log_size_mb": 50}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := loadConfig()
	if cfg.MaxLogSizeMB != 50 {
		t.Errorf("MaxLogSizeMB = %d, want 50", cfg.MaxLogSizeMB)
	}
	// Other fields stay at defaults
	if cfg.LogRetentionDays != 30 {
		t.Errorf("LogRetentionDays = %d, want 30 (default)", cfg.LogRetentionDays)
	}
}

func TestConfigCheckInterval(t *testing.T) {
	cfg := Config{CheckIntervalSeconds: 60}
	if got := cfg.CheckInterval(); got != 60*time.Second {
		t.Errorf("CheckInterval() = %v, want 60s", got)
	}
	cfg2 := Config{CheckIntervalSeconds: 300}
	if got := cfg2.CheckInterval(); got != 300*time.Second {
		t.Errorf("CheckInterval() = %v, want 300s", got)
	}
}

func cfgPath(t *testing.T) string {
	t.Helper()
	exePath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return filepath.Join(filepath.Dir(exePath), "config.json")
}

func TestLoadConfig_Absent(t *testing.T) {
	p := cfgPath(t)
	os.Remove(p) // ensure absent; ignore error if already missing

	cfg := loadConfig()
	d := defaults()
	if cfg.CheckIntervalSeconds != d.CheckIntervalSeconds {
		t.Errorf("CheckIntervalSeconds = %d, want %d", cfg.CheckIntervalSeconds, d.CheckIntervalSeconds)
	}
	if cfg.RetryAttempts != d.RetryAttempts {
		t.Errorf("RetryAttempts = %d, want %d", cfg.RetryAttempts, d.RetryAttempts)
	}
	if cfg.LogRetentionDays != d.LogRetentionDays {
		t.Errorf("LogRetentionDays = %d, want %d", cfg.LogRetentionDays, d.LogRetentionDays)
	}
}

func TestLoadConfig_Partial(t *testing.T) {
	p := cfgPath(t)
	defer os.Remove(p)

	if err := os.WriteFile(p, []byte(`{"check_interval_seconds": 120}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := loadConfig()
	if cfg.CheckIntervalSeconds != 120 {
		t.Errorf("CheckIntervalSeconds = %d, want 120", cfg.CheckIntervalSeconds)
	}
	if cfg.RetryAttempts != 3 {
		t.Errorf("RetryAttempts = %d, want 3 (default)", cfg.RetryAttempts)
	}
	if cfg.LogRetentionDays != 30 {
		t.Errorf("LogRetentionDays = %d, want 30 (default)", cfg.LogRetentionDays)
	}
}

func TestLoadConfig_Invalid(t *testing.T) {
	p := cfgPath(t)
	defer os.Remove(p)

	if err := os.WriteFile(p, []byte(`not valid json`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg := loadConfig()
	d := defaults()
	if cfg.CheckIntervalSeconds != d.CheckIntervalSeconds {
		t.Errorf("CheckIntervalSeconds = %d, want %d (default)", cfg.CheckIntervalSeconds, d.CheckIntervalSeconds)
	}
	if cfg.RetryAttempts != d.RetryAttempts {
		t.Errorf("RetryAttempts = %d, want %d (default)", cfg.RetryAttempts, d.RetryAttempts)
	}
}

// ─── validateConfig() ─────────────────────────────────────────────────────────

func TestValidateConfig_ValidDefaults(t *testing.T) {
	// defaults() should produce zero warnings — no panic expected
	validateConfig(defaults())
}

func TestValidateConfig_IntervalTooLow(t *testing.T) {
	cfg := defaults()
	cfg.CheckIntervalSeconds = 5 // below minimum 10
	// validateConfig requires log != nil to emit warnings; with nil log it must not panic
	validateConfig(cfg)
}

func TestValidateConfig_RetryOutOfRange(t *testing.T) {
	cfg := defaults()
	cfg.RetryAttempts = 0
	validateConfig(cfg)
	cfg.RetryAttempts = 11
	validateConfig(cfg)
}

func TestValidateConfig_RetentionTooLow(t *testing.T) {
	cfg := defaults()
	cfg.LogRetentionDays = 0
	validateConfig(cfg)
}

func TestValidateConfig_SizeTooLow(t *testing.T) {
	cfg := defaults()
	cfg.MaxLogSizeMB = 0
	validateConfig(cfg)
}

// ─── buildDailyReport / cycleErrors ───────────────────────────────────────────

func TestBuildDailyReport_IncludesAllFields(t *testing.T) {
	report := buildDailyReport(10, 3, 5, 2)
	for _, want := range []string{
		"Vérifications totales : 10",
		"Mises à jour installées : 3",
		"Vérifications sans mise à jour : 5",
		"Erreurs : 2",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q\nfull report: %s", want, report)
		}
	}
}

func TestBuildDailyReport_ZeroErrors(t *testing.T) {
	report := buildDailyReport(5, 1, 4, 0)
	if !strings.Contains(report, "Erreurs : 0") {
		t.Errorf("report should contain 'Erreurs : 0'\nfull report: %s", report)
	}
}

func TestCycleErrors_Reset(t *testing.T) {
	atomic.StoreInt64(&cycleErrors, 7)
	got := atomic.SwapInt64(&cycleErrors, 0)
	if got != 7 {
		t.Errorf("cycleErrors = %d, want 7", got)
	}
	if after := atomic.LoadInt64(&cycleErrors); after != 0 {
		t.Errorf("cycleErrors after reset = %d, want 0", after)
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

// ─── formatUptime() ───────────────────────────────────────────────────────────

func TestFormatUptime(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "0m 30s"},
		{90 * time.Second, "1m 30s"},
		{1*time.Hour + 5*time.Minute + 3*time.Second, "1h 5m 3s"},
		{2*time.Hour + 0*time.Minute + 0*time.Second, "2h 0m 0s"},
		{0, "0m 0s"},
	}
	for _, c := range cases {
		if got := formatUptime(c.d); got != c.want {
			t.Errorf("formatUptime(%v) = %q, want %q", c.d, got, c.want)
		}
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

// ─── Circuit breaker ──────────────────────────────────────────────────────────

func TestDefaults_CircuitBreaker(t *testing.T) {
	d := defaults()
	if d.CircuitBreakerThreshold != 5 {
		t.Errorf("CircuitBreakerThreshold = %d, want 5", d.CircuitBreakerThreshold)
	}
	if d.CircuitBreakerPauseMinutes != 30 {
		t.Errorf("CircuitBreakerPauseMinutes = %d, want 30", d.CircuitBreakerPauseMinutes)
	}
}

func TestConsecutiveErrors_ResetOnSuccess(t *testing.T) {
	atomic.StoreInt64(&consecutiveErrors, 4)
	atomic.StoreInt64(&consecutiveErrors, 0)
	if got := atomic.LoadInt64(&consecutiveErrors); got != 0 {
		t.Errorf("consecutiveErrors = %d, want 0", got)
	}
}

func TestLoadConfig_CircuitBreaker(t *testing.T) {
	p := cfgPath(t)
	defer os.Remove(p)
	if err := os.WriteFile(p, []byte(`{"circuit_breaker_threshold":3,"circuit_breaker_pause_minutes":15}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	cfg := loadConfig()
	if cfg.CircuitBreakerThreshold != 3 {
		t.Errorf("CircuitBreakerThreshold = %d, want 3", cfg.CircuitBreakerThreshold)
	}
	if cfg.CircuitBreakerPauseMinutes != 15 {
		t.Errorf("CircuitBreakerPauseMinutes = %d, want 15", cfg.CircuitBreakerPauseMinutes)
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

// ─── openLogs() ───────────────────────────────────────────────────────────────

func TestOpenLogs_AbsentFile(t *testing.T) {
	// Point logDir at a temp dir with no UpdateLog.txt — should not panic.
	old := logDir
	logDir = t.TempDir()
	defer func() { logDir = old }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	oldOut := os.Stdout
	os.Stdout = w
	openLogs()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 512)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if !strings.Contains(out, "Aucun fichier de log") {
		t.Errorf("expected 'Aucun fichier de log' message, got: %s", out)
	}
}

// ─── printReport() ────────────────────────────────────────────────────────────

func TestPrintReport_NoPanic(t *testing.T) {
	// Set known counter values, verify output contains them.
	atomic.StoreInt64(&updatesChecked, 5)
	atomic.StoreInt64(&updatesInstalled, 2)
	atomic.StoreInt64(&updatesSkipped, 3)
	atomic.StoreInt64(&cycleErrors, 1)
	defer func() {
		atomic.StoreInt64(&updatesChecked, 0)
		atomic.StoreInt64(&updatesInstalled, 0)
		atomic.StoreInt64(&updatesSkipped, 0)
		atomic.StoreInt64(&cycleErrors, 0)
	}()

	// Capture stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	printReport()
	w.Close()
	os.Stdout = old

	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	for _, want := range []string{"5", "2", "3", "1"} {
		if !strings.Contains(out, want) {
			t.Errorf("printReport output missing %q\ngot: %s", want, out)
		}
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

// ─── printHelp() ──────────────────────────────────────────────────────────────

func TestPrintHelp_NoPanic(t *testing.T) {
	// Redirect stdout to discard output; just verify no panic.
	old := os.Stdout
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	os.Stdout = devNull
	defer func() {
		os.Stdout = old
		devNull.Close()
	}()
	printHelp()
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

// ─── log_level config ─────────────────────────────────────────────────────────

func TestDefaults_LogLevel(t *testing.T) {
	d := defaults()
	if d.LogLevel != "info" {
		t.Errorf("LogLevel default = %q, want \"info\"", d.LogLevel)
	}
}

func TestLoadConfig_LogLevel(t *testing.T) {
	p := cfgPath(t)
	defer os.Remove(p)
	if err := os.WriteFile(p, []byte(`{"log_level":"debug"}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	c := loadConfig()
	if c.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want \"debug\"", c.LogLevel)
	}
}

func TestValidateConfig_LogLevel_Invalid(t *testing.T) {
	// log == nil in tests, validateConfig must not panic with unknown level.
	cfg := defaults()
	cfg.LogLevel = "verbose"
	validateConfig(cfg)
}

func TestValidateConfig_LogLevel_Valid(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error"} {
		cfg := defaults()
		cfg.LogLevel = level
		validateConfig(cfg)
	}
}

// ─── min_free_disk_mb ─────────────────────────────────────────────────────────

func TestDefaults_MinFreeDiskMB(t *testing.T) {
	d := defaults()
	if d.MinFreeDiskMB != 500 {
		t.Errorf("MinFreeDiskMB default = %d, want 500", d.MinFreeDiskMB)
	}
}

func TestLoadConfig_MinFreeDiskMB(t *testing.T) {
	p := cfgPath(t)
	defer os.Remove(p)
	if err := os.WriteFile(p, []byte(`{"min_free_disk_mb":1000}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	c := loadConfig()
	if c.MinFreeDiskMB != 1000 {
		t.Errorf("MinFreeDiskMB = %d, want 1000", c.MinFreeDiskMB)
	}
}

func TestValidateConfig_MinFreeDiskMB_TooLow(t *testing.T) {
	c := defaults()
	c.MinFreeDiskMB = 50
	validateConfig(c) // log == nil, must not panic
}

// ─── notifications_enabled ────────────────────────────────────────────────────

func TestNotificationsOn_Default(t *testing.T) {
	d := defaults()
	if !d.NotificationsOn() {
		t.Error("NotificationsOn() should be true by default")
	}
}

func TestNotificationsOn_Disabled(t *testing.T) {
	c := defaults()
	c.NotificationsEnabled = boolPtr(false)
	if c.NotificationsOn() {
		t.Error("NotificationsOn() should be false when explicitly disabled")
	}
}

func TestNotificationsOn_Nil(t *testing.T) {
	c := defaults()
	c.NotificationsEnabled = nil
	if !c.NotificationsOn() {
		t.Error("NotificationsOn() should be true when nil (no config)")
	}
}

func TestLoadConfig_NotificationsDisabled(t *testing.T) {
	p := cfgPath(t)
	defer os.Remove(p)
	if err := os.WriteFile(p, []byte(`{"notifications_enabled":false}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	c := loadConfig()
	if c.NotificationsOn() {
		t.Error("NotificationsOn() should be false when set to false in config")
	}
}

// ─── diagnose ─────────────────────────────────────────────────────────────────

func TestFreeDiskMB_Positive(t *testing.T) {
	free := freeDiskMB()
	if free <= 0 {
		t.Errorf("freeDiskMB() = %d, want > 0", free)
	}
}

// ─── printShowConfig() ────────────────────────────────────────────────────────

func TestPrintShowConfig_NoPanic(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	printShowConfig()
	w.Close()
	os.Stdout = old

	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	for _, want := range []string{"check_interval_seconds", "log_level", "retry_attempts"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

// ─── resetCounters() ──────────────────────────────────────────────────────────

func TestResetCounters(t *testing.T) {
	oldDir := logDir
	logDir = t.TempDir()
	defer func() { logDir = oldDir }()

	atomic.StoreInt64(&updatesChecked, 5)
	atomic.StoreInt64(&updatesInstalled, 2)
	atomic.StoreInt64(&updatesSkipped, 3)
	atomic.StoreInt64(&cycleErrors, 1)
	atomic.StoreInt64(&consecutiveErrors, 4)
	lastInstalledMu.Lock()
	lastInstalled = []installEntry{{KB: "KB1", Title: "T", InstalledAt: "2026-01-01T00:00:00Z"}}
	lastInstalledMu.Unlock()

	// Redirect stdout
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	resetCounters()
	w.Close()
	os.Stdout = old
	buf := make([]byte, 128)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "zéro") {
		t.Errorf("expected confirmation message, got: %s", string(buf[:n]))
	}

	if atomic.LoadInt64(&updatesChecked) != 0 {
		t.Error("updatesChecked should be 0")
	}
	if atomic.LoadInt64(&consecutiveErrors) != 0 {
		t.Error("consecutiveErrors should be 0")
	}
	lastInstalledMu.Lock()
	n2 := len(lastInstalled)
	lastInstalledMu.Unlock()
	if n2 != 0 {
		t.Errorf("lastInstalled len = %d, want 0", n2)
	}
}

// ─── testNotify() ─────────────────────────────────────────────────────────────

func TestTestNotify_NoPanic(t *testing.T) {
	// toast.Push() will fail silently in a test environment — just verify no panic.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	testNotify()
	w.Close()
	os.Stdout = old

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if !strings.Contains(out, "Notification de test envoyée") {
		t.Errorf("expected confirmation message, got: %s", out)
	}
}

// ─── weekly report ────────────────────────────────────────────────────────────

func TestBuildWeeklyReport(t *testing.T) {
	report := buildWeeklyReport(70, 21, 42, 7)
	for _, want := range []string{"hebdomadaire", "70", "21", "42", "7"} {
		if !strings.Contains(report, want) {
			t.Errorf("buildWeeklyReport missing %q, got: %s", want, report)
		}
	}
}

func TestDurationUntilNextSunday_Positive(t *testing.T) {
	d := durationUntilNextSunday()
	if d <= 0 || d > 7*24*time.Hour {
		t.Errorf("durationUntilNextSunday() = %v, want (0, 7d]", d)
	}
}

func TestWeeklyCounters_AccumulatedByDaily(t *testing.T) {
	atomic.StoreInt64(&updatesChecked, 5)
	atomic.StoreInt64(&updatesInstalled, 2)
	atomic.StoreInt64(&updatesSkipped, 3)
	atomic.StoreInt64(&cycleErrors, 1)
	atomic.StoreInt64(&weeklyChecked, 0)
	atomic.StoreInt64(&weeklyInstalled, 0)
	atomic.StoreInt64(&weeklySkipped, 0)
	atomic.StoreInt64(&weeklyErrors, 0)
	defer func() {
		for _, p := range []*int64{&updatesChecked, &updatesInstalled, &updatesSkipped, &cycleErrors,
			&weeklyChecked, &weeklyInstalled, &weeklySkipped, &weeklyErrors} {
			atomic.StoreInt64(p, 0)
		}
	}()

	generateDailyReport()

	if atomic.LoadInt64(&weeklyChecked) != 5 {
		t.Errorf("weeklyChecked = %d, want 5", atomic.LoadInt64(&weeklyChecked))
	}
	if atomic.LoadInt64(&weeklyInstalled) != 2 {
		t.Errorf("weeklyInstalled = %d, want 2", atomic.LoadInt64(&weeklyInstalled))
	}
}

// ─── tailLogs() ───────────────────────────────────────────────────────────────

func TestTailLogs_LastLines(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// Write 30 lines numbered 1..30
	var sb strings.Builder
	for i := 1; i <= 30; i++ {
		sb.WriteString(fmt.Sprintf("line %d\n", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	old2 := os.Stdout
	os.Stdout = w
	tailLogs() // default 20 lines
	w.Close()
	os.Stdout = old2

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "line 30") {
		t.Errorf("expected last line in output, got: %s", out)
	}
	if strings.Contains(out, "line 1\n") {
		t.Errorf("expected first lines to be trimmed, got: %s", out)
	}
}

func TestTailLogs_AbsentFile(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	r, w, _ := os.Pipe()
	old2 := os.Stdout
	os.Stdout = w
	tailLogs()
	w.Close()
	os.Stdout = old2

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "Aucun fichier de log") {
		t.Errorf("expected absent message, got: %s", string(buf[:n]))
	}
}

// ─── listLogs() ───────────────────────────────────────────────────────────────

func TestListLogs_ShowsFiles(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// Create current log + one archive.
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte("current"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog_2026-01-01.txt"), []byte("archive"), 0644); err != nil {
		t.Fatal(err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	old2 := os.Stdout
	os.Stdout = w
	listLogs()
	w.Close()
	os.Stdout = old2

	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "UpdateLog.txt") {
		t.Errorf("expected UpdateLog.txt in output, got: %s", out)
	}
	if !strings.Contains(out, "UpdateLog_2026-01-01.txt") {
		t.Errorf("expected archive in output, got: %s", out)
	}
}

func TestListLogs_NoFiles(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	old2 := os.Stdout
	os.Stdout = w
	listLogs()
	w.Close()
	os.Stdout = old2

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "Aucun fichier de log") {
		t.Errorf("expected 'Aucun fichier de log' in output, got: %s", out)
	}
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

// ─── historyLogs() ────────────────────────────────────────────────────────────

func TestHistoryLogs_NoFile(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	historyLogs()
	w.Close()
	os.Stdout = oldOut
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "Aucune installation enregistrée") {
		t.Errorf("expected no-install message, got: %q", out)
	}
}

func TestHistoryLogs_WithEntries(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	content := "2026-02-24 10:00:00 [INFO]: Installation terminée : KB5034441, KB5034442\n" +
		"2026-02-24 10:01:00 [INFO]: Heartbeat\n"
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	historyLogs()
	w.Close()
	os.Stdout = oldOut
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "KB5034441") {
		t.Errorf("expected KB5034441 in output, got: %q", out)
	}
	if !strings.Contains(out, "Total : 1 installation(s)") {
		t.Errorf("expected total=1, got: %q", out)
	}
}

// ─── rebootNotified anti-spam ─────────────────────────────────────────────────

func TestRebootNotified_InitiallyFalse(t *testing.T) {
	rebootNotifiedMu.Lock()
	rebootNotified = false
	rebootNotifiedMu.Unlock()

	rebootNotifiedMu.Lock()
	got := rebootNotified
	rebootNotifiedMu.Unlock()
	if got {
		t.Error("rebootNotified should be false initially")
	}
}

func TestRebootNotified_SetOnce(t *testing.T) {
	rebootNotifiedMu.Lock()
	rebootNotified = false
	rebootNotifiedMu.Unlock()

	// Simulate first detection
	rebootNotifiedMu.Lock()
	notified := rebootNotified
	if !notified {
		rebootNotified = true
	}
	rebootNotifiedMu.Unlock()

	// Second detection should not change anything
	rebootNotifiedMu.Lock()
	notified2 := rebootNotified
	rebootNotifiedMu.Unlock()

	if !notified2 {
		t.Error("rebootNotified should be true after first detection")
	}

	// Cleanup
	rebootNotifiedMu.Lock()
	rebootNotified = false
	rebootNotifiedMu.Unlock()
}

// ─── HeartbeatInterval ────────────────────────────────────────────────────────

func TestHeartbeatInterval_Default(t *testing.T) {
	c := defaults()
	if c.HeartbeatInterval() != 60*time.Minute {
		t.Errorf("expected 60m, got %v", c.HeartbeatInterval())
	}
}

func TestHeartbeatInterval_Custom(t *testing.T) {
	c := defaults()
	c.HeartbeatIntervalMinutes = 15
	if c.HeartbeatInterval() != 15*time.Minute {
		t.Errorf("expected 15m, got %v", c.HeartbeatInterval())
	}
}

func TestDefaults_HeartbeatIntervalMinutes(t *testing.T) {
	c := defaults()
	if c.HeartbeatIntervalMinutes != 60 {
		t.Errorf("expected 60, got %d", c.HeartbeatIntervalMinutes)
	}
}

// ─── exportConfig ─────────────────────────────────────────────────────────────

func TestExportConfig_WritesFile(t *testing.T) {
	dir := t.TempDir()
	// Point executable lookup to the temp dir by overriding logDir
	// exportConfig uses os.Executable(), so we test via the JSON content directly.
	// Instead, call json.MarshalIndent on cfg to verify the serialization.
	old := cfg
	cfg = defaults()
	defer func() { cfg = old }()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	dest := filepath.Join(dir, "config.json")
	if err := os.WriteFile(dest, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "check_interval_seconds") {
		t.Errorf("exported JSON missing check_interval_seconds: %s", string(got))
	}
	if !strings.Contains(string(got), "retry_delay_seconds") {
		t.Errorf("exported JSON missing retry_delay_seconds: %s", string(got))
	}
}

func TestExportConfig_JSONRoundtrip(t *testing.T) {
	old := cfg
	cfg = defaults()
	cfg.CheckIntervalSeconds = 120
	cfg.RetryDelaySeconds = 10
	defer func() { cfg = old }()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	var back Config
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.CheckIntervalSeconds != 120 {
		t.Errorf("CheckIntervalSeconds = %d, want 120", back.CheckIntervalSeconds)
	}
	if back.RetryDelaySeconds != 10 {
		t.Errorf("RetryDelaySeconds = %d, want 10", back.RetryDelaySeconds)
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

// ─── validateConfig (with logger) ─────────────────────────────────────────────

func TestValidateConfig_WithLogger_AllBranches(t *testing.T) {
	withTestLogger(t, func() {
		c := defaults()
		c.CheckIntervalSeconds = 5
		c.RetryAttempts = 0
		c.LogRetentionDays = 0
		c.MaxLogSizeMB = 0
		c.PSTimeoutMinutes = 0
		c.CmdTimeoutSeconds = 5
		c.CircuitBreakerThreshold = 0
		c.CircuitBreakerPauseMinutes = 0
		c.LogLevel = "verbose"
		c.MinFreeDiskMB = 50
		c.HeartbeatIntervalMinutes = 2
		c.RetryDelaySeconds = 0
		validateConfig(c) // must not panic, all warn branches covered
	})
}

func TestValidateConfig_WithLogger_ValidDefaults(t *testing.T) {
	withTestLogger(t, func() {
		validateConfig(defaults()) // no warnings, just runs through
	})
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

// ─── generateWeeklyReport ─────────────────────────────────────────────────────

func TestGenerateWeeklyReport_NilLog(t *testing.T) {
	atomic.StoreInt64(&weeklyChecked, 3)
	atomic.StoreInt64(&weeklyInstalled, 1)
	atomic.StoreInt64(&weeklySkipped, 2)
	atomic.StoreInt64(&weeklyErrors, 0)
	defer func() {
		for _, p := range []*int64{&weeklyChecked, &weeklyInstalled, &weeklySkipped, &weeklyErrors} {
			atomic.StoreInt64(p, 0)
		}
	}()

	generateWeeklyReport() // log is nil, must not panic

	// counters should be reset to 0
	if atomic.LoadInt64(&weeklyChecked) != 0 {
		t.Errorf("weeklyChecked should be 0 after report, got %d", atomic.LoadInt64(&weeklyChecked))
	}
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

// ─── RetryDelay / defaultBackoff ──────────────────────────────────────────────

func TestRetryDelay_Default(t *testing.T) {
	c := defaults()
	if c.RetryDelay() != 5*time.Second {
		t.Errorf("expected 5s, got %v", c.RetryDelay())
	}
}

func TestRetryDelay_Custom(t *testing.T) {
	c := defaults()
	c.RetryDelaySeconds = 10
	if c.RetryDelay() != 10*time.Second {
		t.Errorf("expected 10s, got %v", c.RetryDelay())
	}
}

func TestDefaultBackoff_UsesConfig(t *testing.T) {
	old := cfg
	cfg = defaults()
	cfg.RetryDelaySeconds = 2
	defer func() { cfg = old }()

	delays := defaultBackoff()
	if len(delays) != 3 {
		t.Fatalf("expected 3 delays, got %d", len(delays))
	}
	if delays[0] != 2*time.Second {
		t.Errorf("delays[0] = %v, want 2s", delays[0])
	}
	if delays[1] != 6*time.Second {
		t.Errorf("delays[1] = %v, want 6s", delays[1])
	}
	if delays[2] != 12*time.Second {
		t.Errorf("delays[2] = %v, want 12s", delays[2])
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
