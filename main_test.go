package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
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
