package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Config holds runtime parameters loaded from config.json.
// All fields are optional — missing keys fall back to defaults.
type Config struct {
	CheckIntervalSeconds int `json:"check_interval_seconds"`
	RetryAttempts        int `json:"retry_attempts"`
	LogRetentionDays     int `json:"log_retention_days"`
}

// defaults returns a Config populated with the built-in default values.
func defaults() Config {
	return Config{
		CheckIntervalSeconds: 60,
		RetryAttempts:        3,
		LogRetentionDays:     30,
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

	return cfg
}

// CheckInterval returns the update check interval as a time.Duration.
func (c Config) CheckInterval() time.Duration {
	return time.Duration(c.CheckIntervalSeconds) * time.Second
}
