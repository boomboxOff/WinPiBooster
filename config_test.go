package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// cfgPath returns the path to config.json next to the test executable.
func cfgPath(t *testing.T) string {
	t.Helper()
	exePath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	return filepath.Join(filepath.Dir(exePath), "config.json")
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

func TestLoadConfig_Invalid_WithLogger(t *testing.T) {
	p := cfgPath(t)
	defer os.Remove(p)
	if err := os.WriteFile(p, []byte(`not valid json`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	withTestLogger(t, func() {
		c := loadConfig()
		d := defaults()
		if c.CheckIntervalSeconds != d.CheckIntervalSeconds {
			t.Errorf("CheckIntervalSeconds = %d, want %d (default)", c.CheckIntervalSeconds, d.CheckIntervalSeconds)
		}
	})
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
