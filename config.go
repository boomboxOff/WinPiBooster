package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// boolPtr returns a pointer to the given bool value.
func boolPtr(b bool) *bool { return &b }

// validLogLevels lists the accepted values for the log_level config field.
var validLogLevels = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
}

// Config holds runtime parameters loaded from config.json.
// All fields are optional — missing keys fall back to defaults.
type Config struct {
	CheckIntervalSeconds       int `json:"check_interval_seconds"`
	RetryAttempts              int `json:"retry_attempts"`
	LogRetentionDays           int `json:"log_retention_days"`
	MaxLogSizeMB               int `json:"max_log_size_mb"`
	PSTimeoutMinutes           int `json:"ps_timeout_minutes"`
	CmdTimeoutSeconds          int `json:"cmd_timeout_seconds"`
	CircuitBreakerThreshold    int    `json:"circuit_breaker_threshold"`
	CircuitBreakerPauseMinutes int    `json:"circuit_breaker_pause_minutes"`
	LogLevel                   string `json:"log_level"`
	NotificationsEnabled       *bool  `json:"notifications_enabled"`
	MinFreeDiskMB              int    `json:"min_free_disk_mb"`
	HeartbeatIntervalMinutes   int    `json:"heartbeat_interval_minutes"`
}

// defaults returns a Config populated with the built-in default values.
func defaults() Config {
	return Config{
		CheckIntervalSeconds:       60,
		RetryAttempts:              3,
		LogRetentionDays:           30,
		MaxLogSizeMB:               10,
		PSTimeoutMinutes:           10,
		CmdTimeoutSeconds:          300,
		CircuitBreakerThreshold:    5,
		CircuitBreakerPauseMinutes: 30,
		LogLevel:                   "info",
		NotificationsEnabled:       boolPtr(true),
		MinFreeDiskMB:              500,
		HeartbeatIntervalMinutes:   60,
	}
}

// loadConfig reads config.json from the executable directory.
// Missing file or missing keys are silently replaced by defaults.
func loadConfig() Config {
	cfg := defaults()

	exePath, err := os.Executable()
	if err != nil {
		return cfg
	}
	cfgPath := filepath.Join(filepath.Dir(exePath), "config.json")

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		// File absent — use defaults silently
		return cfg
	}

	// Partial unmarshal: only override fields present in the file
	var partial Config
	if err := json.Unmarshal(data, &partial); err != nil {
		if log != nil {
			log.Warnf("config.json invalide, valeurs par défaut utilisées : %v", err)
		}
		return cfg
	}

	if partial.CheckIntervalSeconds > 0 {
		cfg.CheckIntervalSeconds = partial.CheckIntervalSeconds
	}
	if partial.RetryAttempts > 0 {
		cfg.RetryAttempts = partial.RetryAttempts
	}
	if partial.LogRetentionDays > 0 {
		cfg.LogRetentionDays = partial.LogRetentionDays
	}
	if partial.MaxLogSizeMB > 0 {
		cfg.MaxLogSizeMB = partial.MaxLogSizeMB
	}
	if partial.PSTimeoutMinutes > 0 {
		cfg.PSTimeoutMinutes = partial.PSTimeoutMinutes
	}
	if partial.CmdTimeoutSeconds > 0 {
		cfg.CmdTimeoutSeconds = partial.CmdTimeoutSeconds
	}
	if partial.CircuitBreakerThreshold > 0 {
		cfg.CircuitBreakerThreshold = partial.CircuitBreakerThreshold
	}
	if partial.CircuitBreakerPauseMinutes > 0 {
		cfg.CircuitBreakerPauseMinutes = partial.CircuitBreakerPauseMinutes
	}
	if partial.LogLevel != "" {
		cfg.LogLevel = partial.LogLevel
	}
	if partial.NotificationsEnabled != nil {
		cfg.NotificationsEnabled = partial.NotificationsEnabled
	}
	if partial.MinFreeDiskMB > 0 {
		cfg.MinFreeDiskMB = partial.MinFreeDiskMB
	}
	if partial.HeartbeatIntervalMinutes > 0 {
		cfg.HeartbeatIntervalMinutes = partial.HeartbeatIntervalMinutes
	}

	return cfg
}

// NotificationsOn returns true if toast notifications are enabled (default: true).
func (c Config) NotificationsOn() bool {
	return c.NotificationsEnabled == nil || *c.NotificationsEnabled
}

// CheckInterval returns the update check interval as a time.Duration.
func (c Config) CheckInterval() time.Duration {
	return time.Duration(c.CheckIntervalSeconds) * time.Second
}

// PSTimeout returns the PowerShell command timeout as a time.Duration.
func (c Config) PSTimeout() time.Duration {
	return time.Duration(c.PSTimeoutMinutes) * time.Minute
}

// CmdTimeout returns the system command timeout as a time.Duration.
func (c Config) CmdTimeout() time.Duration {
	return time.Duration(c.CmdTimeoutSeconds) * time.Second
}

// HeartbeatInterval returns the heartbeat interval as a time.Duration.
func (c Config) HeartbeatInterval() time.Duration {
	return time.Duration(c.HeartbeatIntervalMinutes) * time.Minute
}

// validateConfig logs a WARN for each field that is outside its acceptable range.
// It does NOT modify cfg — callers should rely on loadConfig() defaults instead.
func validateConfig(cfg Config) {
	if log == nil {
		return
	}
	if cfg.CheckIntervalSeconds < 10 {
		log.Warnf("config.json : check_interval_seconds=%d est trop bas (minimum 10) — valeur par défaut utilisée : 60", cfg.CheckIntervalSeconds)
	}
	if cfg.RetryAttempts < 1 || cfg.RetryAttempts > 10 {
		log.Warnf("config.json : retry_attempts=%d hors plage [1-10] — valeur par défaut utilisée : 3", cfg.RetryAttempts)
	}
	if cfg.LogRetentionDays < 1 {
		log.Warnf("config.json : log_retention_days=%d est trop bas (minimum 1) — valeur par défaut utilisée : 30", cfg.LogRetentionDays)
	}
	if cfg.MaxLogSizeMB < 1 {
		log.Warnf("config.json : max_log_size_mb=%d est trop bas (minimum 1) — valeur par défaut utilisée : 10", cfg.MaxLogSizeMB)
	}
	if cfg.PSTimeoutMinutes < 1 {
		log.Warnf("config.json : ps_timeout_minutes=%d est trop bas (minimum 1) — valeur par défaut utilisée : 10", cfg.PSTimeoutMinutes)
	}
	if cfg.CmdTimeoutSeconds < 10 {
		log.Warnf("config.json : cmd_timeout_seconds=%d est trop bas (minimum 10) — valeur par défaut utilisée : 300", cfg.CmdTimeoutSeconds)
	}
	if cfg.CircuitBreakerThreshold < 1 {
		log.Warnf("config.json : circuit_breaker_threshold=%d est trop bas (minimum 1) — valeur par défaut utilisée : 5", cfg.CircuitBreakerThreshold)
	}
	if cfg.CircuitBreakerPauseMinutes < 1 {
		log.Warnf("config.json : circuit_breaker_pause_minutes=%d est trop bas (minimum 1) — valeur par défaut utilisée : 30", cfg.CircuitBreakerPauseMinutes)
	}
	if cfg.LogLevel != "" && !validLogLevels[cfg.LogLevel] {
		log.Warnf("config.json : log_level=%q inconnu (valeurs acceptées : debug, info, warn, error) — valeur par défaut utilisée : info", cfg.LogLevel)
	}
	if cfg.MinFreeDiskMB < 100 {
		log.Warnf("config.json : min_free_disk_mb=%d est trop bas (minimum 100) — valeur par défaut utilisée : 500", cfg.MinFreeDiskMB)
	}
	if cfg.HeartbeatIntervalMinutes < 5 {
		log.Warnf("config.json : heartbeat_interval_minutes=%d est trop bas (minimum 5) — valeur par défaut utilisée : 60", cfg.HeartbeatIntervalMinutes)
	}
}
