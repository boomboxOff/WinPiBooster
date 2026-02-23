package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
