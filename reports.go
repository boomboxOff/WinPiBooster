package main

import (
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

// buildDailyReport formats the daily report string from the given counters.
func buildDailyReport(checked, installed, skipped, errors int64) string {
	return fmt.Sprintf(
		"Rapport quotidien :\n- Vérifications totales : %d\n- Mises à jour installées : %d\n- Vérifications sans mise à jour : %d\n- Erreurs : %d",
		checked, installed, skipped, errors,
	)
}

func generateDailyReport() {
	checked := atomic.SwapInt64(&updatesChecked, 0)
	installed := atomic.SwapInt64(&updatesInstalled, 0)
	skipped := atomic.SwapInt64(&updatesSkipped, 0)
	errors := atomic.SwapInt64(&cycleErrors, 0)

	// Accumulate into weekly counters
	atomic.AddInt64(&weeklyChecked, checked)
	atomic.AddInt64(&weeklyInstalled, installed)
	atomic.AddInt64(&weeklySkipped, skipped)
	atomic.AddInt64(&weeklyErrors, errors)

	report := buildDailyReport(checked, installed, skipped, errors)
	if log != nil {
		log.Info(report)
	}
}

// buildWeeklyReport formats the weekly report string.
func buildWeeklyReport(checked, installed, skipped, errors int64) string {
	return fmt.Sprintf(
		"Rapport hebdomadaire :\n- Vérifications totales : %d\n- Mises à jour installées : %d\n- Vérifications sans mise à jour : %d\n- Erreurs : %d",
		checked, installed, skipped, errors,
	)
}

func generateWeeklyReport() {
	checked := atomic.SwapInt64(&weeklyChecked, 0)
	installed := atomic.SwapInt64(&weeklyInstalled, 0)
	skipped := atomic.SwapInt64(&weeklySkipped, 0)
	errors := atomic.SwapInt64(&weeklyErrors, 0)

	report := buildWeeklyReport(checked, installed, skipped, errors)
	if log != nil {
		log.Info(report)
	}
}

// durationUntilNextSunday returns the duration until the next Sunday midnight.
func durationUntilNextSunday() time.Duration {
	now := time.Now()
	daysUntil := int(time.Sunday - now.Weekday())
	if daysUntil <= 0 {
		daysUntil += 7
	}
	next := time.Date(now.Year(), now.Month(), now.Day()+daysUntil, 0, 0, 0, 0, now.Location())
	return time.Until(next)
}

// scheduleWeeklyReport fires generateWeeklyReport every Sunday at midnight.
func scheduleWeeklyReport() {
	timer := time.NewTimer(durationUntilNextSunday())
	go func() {
		for {
			<-timer.C
			generateWeeklyReport()
			timer.Reset(durationUntilNextSunday())
		}
	}()
}

// formatUptime formats a duration as "Xh Ym Zs" (hours optional if zero).
func formatUptime(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	return fmt.Sprintf("%dm %ds", m, s)
}

func heartbeat() {
	uptime := formatUptime(time.Since(startTime))
	checked := atomic.LoadInt64(&updatesChecked)
	installed := atomic.LoadInt64(&updatesInstalled)
	errors := atomic.LoadInt64(&cycleErrors)
	log.Info(strings.Repeat("─", 62))
	log.Infof("WinPiBooster %s — actif depuis %s | vérifications: %d | installées: %d | erreurs: %d",
		version, uptime, checked, installed, errors)
}

// durationUntilMidnight returns the duration until the next midnight.
func durationUntilMidnight() time.Duration {
	now := time.Now()
	next := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	return time.Until(next)
}

// scheduleDailyReport fires generateDailyReport at midnight and reschedules itself.
func scheduleDailyReport() {
	timer := time.NewTimer(durationUntilMidnight())
	go func() {
		for {
			<-timer.C
			archiveOldLogs()
			generateDailyReport()
			cleanOldLogs()
			timer.Reset(durationUntilMidnight())
		}
	}()
}
