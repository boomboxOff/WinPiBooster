package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"gopkg.in/toast.v1"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z"
var version = "dev"

// psTimeout and cmdTimeout constants removed — use cfg.PSTimeout() / cfg.CmdTimeout().

// ─── Globals ──────────────────────────────────────────────────────────────────

var (
	log           *logrus.Logger
	logHook       *fileHook
	logDir        string // directory of the executable
	cfg           Config

	// Counters (atomic) — reset by daily report
	updatesChecked    int64
	updatesInstalled  int64
	updatesSkipped    int64
	cycleErrors       int64
	consecutiveErrors int64 // reset to 0 on each successful cycle

	// Weekly accumulators — fed by generateDailyReport, reset by generateWeeklyReport
	weeklyChecked   int64
	weeklyInstalled int64
	weeklySkipped   int64
	weeklyErrors    int64

	// Process start time (set at package init).
	startTime time.Time

	// Prevent concurrent update cycles
	cycleMu sync.Mutex

	// History of the last 10 installed updates (for status.json).
	lastInstalled   []installEntry
	lastInstalledMu sync.Mutex

	// Cached flag: PSWindowsUpdate module ready
	psModuleReady bool
	psModuleMu    sync.Mutex

	// Anti-spam flag: reboot-pending notification sent once per session
	rebootNotified   bool
	rebootNotifiedMu sync.Mutex

	// Anti-spam flag: circuit breaker notification sent once per CB activation
	cbNotified   bool
	cbNotifiedMu sync.Mutex

	// Global shutdown context — cancelled on SIGINT/SIGTERM or service stop.
	shutdownCtx, shutdownCancel = context.WithCancel(context.Background())
)

// installEntry records a single installed update for the status.json history.
type installEntry struct {
	KB          string `json:"kb"`
	Title       string `json:"title"`
	InstalledAt string `json:"installed_at"`
}

// recordInstalled appends entries to lastInstalled, capped at 10 (FIFO).
func recordInstalled(entries []installEntry) {
	lastInstalledMu.Lock()
	defer lastInstalledMu.Unlock()
	lastInstalled = append(lastInstalled, entries...)
	if len(lastInstalled) > 10 {
		lastInstalled = lastInstalled[len(lastInstalled)-10:]
	}
}

// Update mirrors the JSON fields returned by Get-WindowsUpdate.
type Update struct {
	Title         string      `json:"Title"`
	KBArticleIDs  interface{} `json:"KBArticleIDs"` // may be string or []string
	Size          interface{} `json:"Size"`
	PSComputerName string     `json:"PSComputerName"`
}

func (u Update) KB() string {
	switch v := u.KBArticleIDs.(type) {
	case string:
		return v
	case []interface{}:
		parts := make([]string, 0, len(v))
		for _, s := range v {
			parts = append(parts, fmt.Sprintf("%v", s))
		}
		return strings.Join(parts, ", ")
	default:
		return fmt.Sprintf("%v", v)
	}
}

func (u Update) Computer() string {
	if u.PSComputerName == "" {
		return "local"
	}
	return u.PSComputerName
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// newCmdCtx creates an exec.Cmd with a context and CREATE_NEW_PROCESS_GROUP.
func newCmdCtx(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	c.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	return c
}

// execCommand runs a shell command through cmd /C with a timeout.
func execCommand(cmd string) (string, error) {
	ctx, cancel := context.WithTimeout(shutdownCtx, cfg.CmdTimeout())
	defer cancel()
	out, err := newCmdCtx(ctx, "cmd", "/C", cmd).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// execPS runs a PowerShell command with UTF-8 encoding enforced and a timeout.
func execPS(psCmd string) (string, error) {
	full := fmt.Sprintf(
		`[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; chcp 65001 | Out-Null; %s`,
		psCmd,
	)
	ctx, cancel := context.WithTimeout(shutdownCtx, cfg.PSTimeout())
	defer cancel()
	out, err := newCmdCtx(ctx, "powershell.exe", "-NoProfile", "-Command", full).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// showNotification sends a Windows toast notification (best-effort).
// Does nothing if notifications are disabled in config.
func showNotification(title, message string) {
	if !cfg.NotificationsOn() {
		return
	}
	n := toast.Notification{
		AppID:   "WinPiBooster",
		Title:   title,
		Message: message,
		Audio:   toast.Default,
	}
	if err := n.Push(); err != nil {
		if log != nil {
			log.Debugf("Notification non envoyée : %v", err)
		}
	}
}

// testNotify sends a test toast notification and prints a confirmation.
func testNotify() {
	showNotification("WinPiBooster — Test", "Les notifications fonctionnent correctement.")
	fmt.Println("Notification de test envoyée.")
}

// ─── Update logic ─────────────────────────────────────────────────────────────

func installNuGetProvider() {
	log.Debug("Vérification et installation du fournisseur NuGet...")
	_, err := execPS("Install-PackageProvider -Name NuGet -MinimumVersion 2.8.5.201 -Force -Confirm:$false")
	if err != nil {
		log.Warnf("Le fournisseur NuGet est peut-être déjà installé : %v", err)
	} else {
		log.Debug("Fournisseur NuGet installé avec succès.")
	}
}

func isPSWindowsUpdateModuleInstalled() bool {
	result, err := execPS("Get-Module -ListAvailable -Name PSWindowsUpdate")
	if err != nil {
		log.Errorf("Erreur lors de la vérification du module PSWindowsUpdate : %v", err)
		return false
	}
	return strings.Contains(result, "PSWindowsUpdate")
}

func installPSWindowsUpdateModule() error {
	psModuleMu.Lock()
	defer psModuleMu.Unlock()

	if psModuleReady {
		return nil
	}

	if isPSWindowsUpdateModuleInstalled() {
		log.Debug("Le module PSWindowsUpdate est déjà installé.")
		psModuleReady = true
		return nil
	}

	// Install NuGet first to avoid interactive prompts
	installNuGetProvider()

	log.Info("Installation du module PSWindowsUpdate...")
	result, err := execPS("Install-Module -Name PSWindowsUpdate -Force -SkipPublisherCheck -Confirm:$false -AllowClobber")
	if err != nil {
		log.Errorf("Erreur lors de l'installation du module PSWindowsUpdate : %v", err)
		showNotification("Erreur", "Erreur lors de l'installation du module PSWindowsUpdate.")
		return err
	}
	if strings.Contains(strings.ToLower(result), "error") {
		msg := "Erreur détectée pendant l'installation : Conflit potentiel avec les politiques de sécurité ou les permissions administratives."
		log.Error(msg)
		showNotification("Erreur", "Installation du module PSWindowsUpdate échouée.")
		return fmt.Errorf("%s", msg)
	}

	log.Info("Module PSWindowsUpdate installé avec succès.")
	showNotification("Succès", "Module PSWindowsUpdate installé.")
	psModuleReady = true
	return nil
}

func ensureWindowsUpdateServiceRunning() error {
	result, err := execCommand("sc query wuauserv")
	if err != nil {
		log.Errorf("Erreur lors du démarrage du service Windows Update : %v", err)
		return err
	}
	if strings.Contains(result, "STATE              : 4  RUNNING") {
		log.Debug("Le service Windows Update est déjà en cours d'exécution.")
		return nil
	}
	log.Info("Démarrage du service Windows Update...")
	if _, err := execCommand("sc start wuauserv"); err != nil {
		log.Errorf("Erreur lors du démarrage du service Windows Update : %v", err)
		return err
	}
	log.Info("Service Windows Update démarré.")
	return nil
}

func checkAvailableUpdates() ([]Update, error) {
	log.Debug("Vérification des mises à jour disponibles...")
	raw, err := execPS("Get-WindowsUpdate -MicrosoftUpdate | ConvertTo-Json -Compress")
	if err != nil {
		log.Errorf("Erreur lors de la vérification des mises à jour : %v", err)
		atomic.AddInt64(&updatesSkipped, 1)
		return nil, err
	}
	if raw == "" {
		log.Debug("Aucune donnée retournée par PowerShell. Aucune mise à jour disponible ou problème détecté.")
		atomic.AddInt64(&updatesSkipped, 1)
		return []Update{}, nil
	}

	// Normalise: wrap single object in array
	trimmed := strings.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		trimmed = "[" + trimmed + "]"
	}

	var updates []Update
	if err := json.Unmarshal([]byte(trimmed), &updates); err != nil {
		log.Errorf("Réponse PowerShell invalide (JSON malformé) : %s", raw[:min(len(raw), 200)])
		atomic.AddInt64(&updatesSkipped, 1)
		return nil, err
	}

	if len(updates) == 0 {
		log.Debug("Aucune mise à jour disponible.")
		atomic.AddInt64(&updatesSkipped, 1)
		return []Update{}, nil
	}

	for _, u := range updates {
		log.Infof("Mise à jour disponible :\n  - Titre : %s\n  - KB : %s\n  - Taille : %v\n  - Ordinateur : %s",
			u.Title, u.KB(), u.Size, u.Computer())
	}
	atomic.AddInt64(&updatesChecked, int64(len(updates)))
	return updates, nil
}

func installUpdates(updates []Update) error {
	log.Info("Installation des mises à jour...")
	_, err := execPS("Install-WindowsUpdate -MicrosoftUpdate -AcceptAll -AutoReboot")
	if err != nil {
		log.Errorf("Erreur lors de l'installation des mises à jour : %v", err)
		return err
	}

	kbs := make([]string, 0, len(updates))
	titles := make([]string, 0, len(updates))
	for _, u := range updates {
		kbs = append(kbs, "KB"+u.KB())
		titles = append(titles, u.Title)
	}
	log.Infof("Installation terminée : %s", strings.Join(kbs, ", "))
	showNotification("Succès", "Mises à jour Windows installées : "+strings.Join(titles, ", "))
	atomic.AddInt64(&updatesInstalled, int64(len(updates)))

	now := time.Now().UTC().Format(time.RFC3339)
	entries := make([]installEntry, 0, len(updates))
	for _, u := range updates {
		entries = append(entries, installEntry{KB: "KB" + u.KB(), Title: u.Title, InstalledAt: now})
	}
	recordInstalled(entries)
	return nil
}

// ─── Log management ───────────────────────────────────────────────────────────

func archiveOldLogs() {
	logPath := filepath.Join(logDir, "UpdateLog.txt")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		return
	}

	ts := time.Now().Format("2006-01-02T15-04-05")
	archivePath := filepath.Join(logDir, "UpdateLog_"+ts+".txt")

	// Close file hook before renaming
	if logHook != nil {
		logHook.Close()
	}

	if err := os.Rename(logPath, archivePath); err != nil {
		fmt.Fprintf(os.Stderr, "archiveOldLogs: rename failed: %v\n", err)
		// Reopen anyway so logging continues
	}

	if logHook != nil {
		if err := logHook.ReopenFile(); err != nil {
			fmt.Fprintf(os.Stderr, "archiveOldLogs: reopen failed: %v\n", err)
		}
	}
	log.Debug("Journal archivé.")
}

func cleanOldLogs() {
	cleanOldLogsVerbose(false)
}

// cleanOldLogsVerbose removes log archives older than the retention period.
// If verbose is true, prints a summary to stdout (used by the clean-logs command).
func cleanOldLogsVerbose(verbose bool) {
	days := cfg.LogRetentionDays
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if log != nil {
			log.Warnf("cleanOldLogs: cannot read dir: %v", err)
		}
		return
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "UpdateLog_") || !strings.HasSuffix(name, ".txt") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			fullPath := filepath.Join(logDir, name)
			if err := os.Remove(fullPath); err != nil {
				if log != nil {
					log.Warnf("cleanOldLogs: cannot remove %s: %v", name, err)
				}
			} else {
				if log != nil {
					log.Debugf("Ancien journal supprimé : %s", name)
				}
				removed++
			}
		}
	}
	if verbose {
		fmt.Printf("Suppression des archives de plus de %d jours...\n%d archive(s) supprimée(s).\n", days, removed)
	}
}

// ─── status.json ──────────────────────────────────────────────────────────────

type statusJSON struct {
	Version          string         `json:"version"`
	LastCheck        string         `json:"last_check"`
	UptimeSeconds    int64          `json:"uptime_seconds"`
	UpdatesChecked   int64          `json:"updates_checked"`
	UpdatesInstalled int64          `json:"updates_installed"`
	UpdatesSkipped   int64          `json:"updates_skipped"`
	CycleErrors      int64          `json:"cycle_errors"`
	LastInstalled    []installEntry `json:"last_installed"`
}

// writeStatusJSON writes current counters to status.json atomically.
func writeStatusJSON() {
	lastInstalledMu.Lock()
	history := make([]installEntry, len(lastInstalled))
	copy(history, lastInstalled)
	lastInstalledMu.Unlock()

	s := statusJSON{
		Version:          version,
		LastCheck:        time.Now().UTC().Format(time.RFC3339),
		UptimeSeconds:    int64(time.Since(startTime).Seconds()),
		UpdatesChecked:   atomic.LoadInt64(&updatesChecked),
		UpdatesInstalled: atomic.LoadInt64(&updatesInstalled),
		UpdatesSkipped:   atomic.LoadInt64(&updatesSkipped),
		CycleErrors:      atomic.LoadInt64(&cycleErrors),
		LastInstalled:    history,
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		log.Debugf("writeStatusJSON: marshal error: %v", err)
		return
	}
	dest := filepath.Join(logDir, "status.json")
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		log.Debugf("writeStatusJSON: write error: %v", err)
		return
	}
	if err := os.Rename(tmp, dest); err != nil {
		log.Debugf("writeStatusJSON: rename error: %v", err)
		_ = os.Remove(tmp)
	}
}

// ─── Reporting ────────────────────────────────────────────────────────────────

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
	showNotification("Rapport quotidien", report)
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
	showNotification("Rapport hebdomadaire", report)
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

// scheduleCircuitBreakerReset starts a periodic ticker that resets consecutiveErrors
// every cfg.CircuitBreakerResetMinutes minutes. No-op if the config value is 0.
func scheduleCircuitBreakerReset() {
	if cfg.CircuitBreakerResetMinutes <= 0 {
		return
	}
	ticker := time.NewTicker(time.Duration(cfg.CircuitBreakerResetMinutes) * time.Minute)
	go func() {
		for {
			select {
			case <-ticker.C:
				prev := atomic.SwapInt64(&consecutiveErrors, 0)
				if prev > 0 && log != nil {
					log.Debugf("Circuit breaker : auto-reset après %d min (%d erreurs consécutives remises à zéro).", cfg.CircuitBreakerResetMinutes, prev)
				}
			case <-shutdownCtx.Done():
				ticker.Stop()
				return
			}
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

// ─── Diagnose ─────────────────────────────────────────────────────────────────

// freeDiskMB returns the free disk space available on C: in MB.
func freeDiskMB() int64 {
	path, _ := windows.UTF16PtrFromString(`C:\`)
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(path, &free, &total, &totalFree); err != nil {
		return 0
	}
	return int64(free) / (1024 * 1024)
}

// runDiagnose checks prerequisites and prints a health report.
// Returns true if all checks pass.
func runDiagnose() bool {
	allOK := true
	check := func(label string, ok bool, detail string) {
		status := "OK   "
		if !ok {
			status = "ERREUR"
			allOK = false
		}
		if detail != "" {
			fmt.Printf("  [%s] %s — %s\n", status, label, detail)
		} else {
			fmt.Printf("  [%s] %s\n", status, label)
		}
	}

	fmt.Printf("Diagnostic WinPiBooster %s :\n\n", version)

	check("Droits administrateur", checkAdminRights() == nil, "")
	check("Module PSWindowsUpdate", isPSWindowsUpdateModuleInstalled(), "")

	out, err := execCommand("sc query wuauserv")
	wuRunning := err == nil && strings.Contains(out, "RUNNING")
	var wuDetail string
	switch {
	case err != nil:
		wuDetail = fmt.Sprintf("erreur sc query: %v", err)
	case strings.Contains(out, "STOPPED"):
		wuDetail = "arrêté — lancez 'sc start wuauserv' en admin"
	case strings.Contains(out, "PAUSED"):
		wuDetail = "en pause"
	case wuRunning:
		wuDetail = "en cours d'exécution"
	default:
		wuDetail = "état inconnu"
	}
	check("Service Windows Update (wuauserv)", wuRunning, wuDetail)

	free := freeDiskMB()
	check("Espace disque libre (C:)", free >= 500, fmt.Sprintf("%d MB disponibles", free))

	fmt.Println()
	if allOK {
		fmt.Println("Tous les prérequis sont satisfaits.")
	} else {
		fmt.Println("Un ou plusieurs prérequis manquants — consultez les détails ci-dessus.")
	}
	return allOK
}

// ─── Admin rights ─────────────────────────────────────────────────────────────

func checkAdminRights() error {
	_, err := execCommand("net session")
	if err != nil {
		return fmt.Errorf("droits administrateur requis")
	}
	return nil
}

// ─── Retry helper ─────────────────────────────────────────────────────────────

// retryBackoff runs fn up to maxAttempts times, waiting backoffDelays between
// attempts. Returns nil on first success, last error otherwise.
func retryBackoff(name string, maxAttempts int, backoffDelays []time.Duration, fn func() error) error {
	var err error
	for i := 0; i < maxAttempts; i++ {
		if err = fn(); err == nil {
			return nil
		}
		if i < maxAttempts-1 {
			delay := backoffDelays[min(i, len(backoffDelays)-1)]
			log.Warnf("%s — tentative %d/%d échouée, nouvel essai dans %s : %v", name, i+1, maxAttempts, delay, err)
			time.Sleep(delay)
		}
	}
	return err
}

// defaultBackoff builds the retry delay sequence from cfg.RetryDelaySeconds.
// Delays are: base, base×3, base×6  (e.g. 5s, 15s, 30s with default base=5s).
func defaultBackoff() []time.Duration {
	base := cfg.RetryDelay()
	return []time.Duration{base, base * 3, base * 6}
}

func retryAttempts() int {
	if cfg.RetryAttempts > 0 {
		return cfg.RetryAttempts
	}
	return 3
}

// ─── Reboot pending ───────────────────────────────────────────────────────────

// parseRebootPending returns true if the PowerShell output contains "True".
func parseRebootPending(out string) bool {
	return strings.Contains(strings.TrimSpace(out), "True")
}

// isRebootPending checks two common registry keys for a pending Windows reboot.
func isRebootPending() bool {
	ps := `$r = $false
$keys = @(
  "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\Component Based Servicing\RebootPending",
  "HKLM:\SOFTWARE\Microsoft\Windows\CurrentVersion\WindowsUpdate\Auto Update\RebootRequired"
)
foreach ($k in $keys) { if (Test-Path $k) { $r = $true } }
$r`
	out, err := execPS(ps)
	if err != nil {
		log.Debugf("isRebootPending: erreur PS : %v", err)
		return false
	}
	return parseRebootPending(out)
}

// ─── Main cycle ───────────────────────────────────────────────────────────────

func runCycle() {
	// TryLock: skip cycle if a previous one is still running
	if !cycleMu.TryLock() {
		log.Debug("Cycle précédent toujours en cours, passage ignoré.")
		return
	}
	defer cycleMu.Unlock()

	// Abort immediately if shutdown was requested while waiting for the lock.
	if shutdownCtx.Err() != nil {
		return
	}

	// Circuit breaker: pause if too many consecutive errors.
	threshold := int64(cfg.CircuitBreakerThreshold)
	if threshold > 0 && atomic.LoadInt64(&consecutiveErrors) >= threshold {
		pause := time.Duration(cfg.CircuitBreakerPauseMinutes) * time.Minute
		msg := fmt.Sprintf("Circuit ouvert (%d erreurs consécutives) — pause de %v avant le prochain cycle.", threshold, pause)
		log.Warn(msg)
		// Anti-spam: notify only on first trigger of each CB activation.
		cbNotifiedMu.Lock()
		if !cbNotified {
			cbNotified = true
			cbNotifiedMu.Unlock()
			showNotification("Circuit ouvert", msg)
		} else {
			cbNotifiedMu.Unlock()
		}
		select {
		case <-time.After(pause):
		case <-shutdownCtx.Done():
			return
		}
		atomic.StoreInt64(&consecutiveErrors, 0)
		// Reset flag so the next CB activation can notify again.
		cbNotifiedMu.Lock()
		cbNotified = false
		cbNotifiedMu.Unlock()
	}

	log.Debug("Lancement du processus de mise à jour Windows...")

	if err := retryBackoff("installPSWindowsUpdateModule", retryAttempts(), defaultBackoff(), installPSWindowsUpdateModule); err != nil {
		atomic.AddInt64(&cycleErrors, 1)
		atomic.AddInt64(&consecutiveErrors, 1)
		log.Errorf("Erreur globale du processus de mise à jour : %v", err)
		showNotification("Erreur", "Erreur globale du processus de mise à jour.")
		return
	}

	if err := retryBackoff("ensureWindowsUpdateServiceRunning", retryAttempts(), defaultBackoff(), ensureWindowsUpdateServiceRunning); err != nil {
		atomic.AddInt64(&cycleErrors, 1)
		atomic.AddInt64(&consecutiveErrors, 1)
		log.Errorf("Erreur globale du processus de mise à jour : %v", err)
		showNotification("Erreur", "Erreur globale du processus de mise à jour.")
		return
	}

	updates, err := checkAvailableUpdates()
	if err != nil {
		atomic.AddInt64(&cycleErrors, 1)
		atomic.AddInt64(&consecutiveErrors, 1)
		// Error already logged inside checkAvailableUpdates
		return
	}

	if len(updates) > 0 {
		if isRebootPending() {
			msg := "Redémarrage en attente détecté — installation des mises à jour reportée au prochain cycle."
			log.Warn(msg)
			rebootNotifiedMu.Lock()
			if !rebootNotified {
				rebootNotified = true
				rebootNotifiedMu.Unlock()
				showNotification("Redémarrage requis", msg)
			} else {
				rebootNotifiedMu.Unlock()
			}
			return
		}
		if min := int64(cfg.MinFreeDiskMB); min > 0 {
			if free := freeDiskMB(); free < min {
				msg := fmt.Sprintf("Espace disque insuffisant (%d MB libres, minimum %d MB) — installation ignorée.", free, min)
				log.Warn(msg)
				showNotification("Espace disque insuffisant", msg)
				atomic.AddInt64(&updatesSkipped, int64(len(updates)))
				return
			}
		}
		err := retryBackoff("installUpdates", retryAttempts(), defaultBackoff(), func() error {
			return installUpdates(updates)
		})
		if err != nil {
			atomic.AddInt64(&cycleErrors, 1)
			atomic.AddInt64(&consecutiveErrors, 1)
			log.Errorf("Erreur globale du processus de mise à jour : %v", err)
			showNotification("Erreur", "Erreur globale du processus de mise à jour.")
			return
		}
	}

	// Successful cycle — reset consecutive error counter.
	atomic.StoreInt64(&consecutiveErrors, 0)
	writeStatusJSON()
	log.Debug("Processus terminé.")
}

// ─── Startup helpers ──────────────────────────────────────────────────────────

// initLogger initialises the logger and sets logDir from the executable path.
func initLogger() error {
	startTime = time.Now()
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine executable path: %w", err)
	}
	logDir = filepath.Dir(exePath)

	// Load config before logger so warnings can be logged
	log, logHook, err = setupLogger()
	if err != nil {
		return err
	}
	cfg = loadConfig()
	validateConfig(cfg)

	// Apply log_level from config unless DEBUG=true overrides it.
	if os.Getenv("DEBUG") != "true" {
		switch cfg.LogLevel {
		case "debug":
			log.SetLevel(logrus.DebugLevel)
		case "warn":
			log.SetLevel(logrus.WarnLevel)
		case "error":
			log.SetLevel(logrus.ErrorLevel)
		default: // "info" or unrecognised
			log.SetLevel(logrus.InfoLevel)
		}
	}

	if cfg != defaults() {
		log.Debugf("Configuration chargée depuis config.json : interval=%ds retries=%d retention=%dj maxsize=%dMB",
			cfg.CheckIntervalSeconds, cfg.RetryAttempts, cfg.LogRetentionDays, cfg.MaxLogSizeMB)
	}

	// Wire size-based log rotation.
	if logHook != nil && cfg.MaxLogSizeMB > 0 {
		logHook.maxSizeBytes = int64(cfg.MaxLogSizeMB) * 1024 * 1024
		logHook.rotateFn = archiveOldLogs
	}

	return nil
}

// runDryRun performs a single update check without installing anything.
// acquireSingleInstanceMutex creates a Windows named mutex so that only one
// interactive or dry-run instance can run at a time.
// The caller must call windows.CloseHandle(h) when done.
func acquireSingleInstanceMutex() (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString("Global\\WinPiBooster")
	if err != nil {
		return 0, fmt.Errorf("cannot encode mutex name: %w", err)
	}
	h, err := windows.CreateMutex(nil, false, name)
	if err == windows.ERROR_ALREADY_EXISTS {
		return 0, fmt.Errorf("une instance de WinPiBooster est déjà en cours d'exécution")
	}
	return h, err
}

func runDryRun() {
	h, err := acquireSingleInstanceMutex()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Erreur:", err)
		os.Exit(1)
	}
	defer windows.CloseHandle(h)

	if err := checkAdminRights(); err != nil {
		fmt.Fprintln(os.Stderr, "Droits administrateur requis.")
		os.Exit(1)
	}
	fmt.Println("[DRY-RUN] Vérification des mises à jour disponibles (aucune installation)...")

	if err := installPSWindowsUpdateModule(); err != nil {
		fmt.Fprintf(os.Stderr, "[DRY-RUN] Erreur module PSWindowsUpdate : %v\n", err)
		os.Exit(1)
	}

	updates, err := checkAvailableUpdates()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[DRY-RUN] Erreur lors de la vérification : %v\n", err)
		os.Exit(1)
	}

	if len(updates) == 0 {
		fmt.Println("[DRY-RUN] Aucune mise à jour disponible.")
		os.Exit(0)
	}
	fmt.Printf("[DRY-RUN] %d mise(s) à jour disponible(s) :\n", len(updates))
	for _, u := range updates {
		fmt.Printf("  - %s (KB%s)\n", u.Title, u.KB())
	}
	// Exit code 2 signals "updates are available" to callers/scripts.
	os.Exit(2)
}

// runInteractive runs the update loop in console mode (SIGINT/SIGTERM aware).
func runInteractive() {
	h, err := acquireSingleInstanceMutex()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Erreur:", err)
		os.Exit(1)
	}
	defer windows.CloseHandle(h)

	if err := checkAdminRights(); err != nil {
		log.Error("Le script doit être exécuté en tant qu'administrateur. Relancez via WinPiBooster.bat en tant qu'administrateur.")
		showNotification("Erreur", "Droits administrateur requis. Relancez en tant qu'administrateur.")
		os.Exit(1)
	}

	showNotification("WinPiBooster démarré", "Surveillance des mises à jour Windows active.")
	archiveOldLogs()
	heartbeat()

	heartbeatTicker := time.NewTicker(cfg.HeartbeatInterval())
	go func() {
		for {
			select {
			case <-heartbeatTicker.C:
				heartbeat()
			case <-shutdownCtx.Done():
				return
			}
		}
	}()

	scheduleDailyReport()
	scheduleWeeklyReport()
	scheduleCircuitBreakerReset()
	go runCycle()

	cycleTicker := time.NewTicker(cfg.CheckInterval())
	go func() {
		for {
			select {
			case <-cycleTicker.C:
				log.Debug("Début d'un nouveau cycle de vérification des mises à jour.")
				go runCycle()
			case <-shutdownCtx.Done():
				return
			}
		}
	}()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	sig := <-sigs
	log.Infof("Arrêt du script demandé (%s).", sig)
	shutdownCancel()
	heartbeatTicker.Stop()
	cycleTicker.Stop()
	showNotification("WinPiBooster arrêté", "La surveillance des mises à jour Windows est inactive.")
}

// ─── Report / Help ────────────────────────────────────────────────────────────

// listLogs prints all log files (current + archives) with size and modification date.
func listLogs() {
	// Current log
	current := filepath.Join(logDir, "UpdateLog.txt")
	entries := []string{current}

	// Archives
	pattern := filepath.Join(logDir, "UpdateLog_*.txt")
	archives, err := filepath.Glob(pattern)
	if err == nil {
		entries = append(entries, archives...)
	}

	found := false
	for _, p := range entries {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		found = true
		fmt.Printf("  %-40s  %8.1f KB  %s\n",
			filepath.Base(p),
			float64(info.Size())/1024.0,
			info.ModTime().Format("2006-01-02 15:04:05"))
	}
	if !found {
		fmt.Printf("Aucun fichier de log dans %s\n", logDir)
	}
}

// tailLogs prints the last N lines of UpdateLog.txt (default 20).
// Supports --lines N anywhere in os.Args.
func tailLogs() {
	n := 20
	args := os.Args[1:]
	for i, arg := range args {
		if arg == "--lines" && i+1 < len(args) {
			if v, err := strconv.Atoi(args[i+1]); err == nil && v > 0 {
				n = v
			}
		}
	}

	logPath := filepath.Join(logDir, "UpdateLog.txt")
	data, err := os.ReadFile(logPath)
	if err != nil {
		fmt.Printf("Aucun fichier de log trouvé : %s\n", logPath)
		return
	}

	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	fmt.Println(strings.Join(lines, "\n"))
}

// historyLogs scans all log files (current + archives) and prints every
// "Installation terminée" line in chronological order.
// Supports --since YYYY-MM-DD to filter entries from a given date (inclusive).
func historyLogs() {
	// Parse --since YYYY-MM-DD from os.Args
	sinceStr := ""
	args := os.Args[1:]
	for i, arg := range args {
		if arg == "--since" && i+1 < len(args) {
			sinceStr = args[i+1]
		}
	}
	var sinceTime time.Time
	if sinceStr != "" {
		t, err := time.Parse("2006-01-02", sinceStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Format --since invalide : %q (attendu YYYY-MM-DD)\n", sinceStr)
			sinceStr = "" // ignore invalid date, show all
		} else {
			sinceTime = t
		}
	}

	current := filepath.Join(logDir, "UpdateLog.txt")
	archives, _ := filepath.Glob(filepath.Join(logDir, "UpdateLog_*.txt"))

	// Build ordered file list: archives first (sorted), then current log
	sort.Strings(archives)
	files := append(archives, current)

	total := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "Installation terminée :") {
				if !sinceTime.IsZero() && len(line) >= 10 {
					lineDate, err := time.Parse("2006-01-02", line[:10])
					if err != nil || lineDate.Before(sinceTime) {
						continue
					}
				}
				fmt.Println(strings.TrimRight(line, "\r"))
				total++
			}
		}
	}
	if total == 0 {
		if sinceStr != "" {
			fmt.Printf("Aucune installation enregistrée depuis le %s.\n", sinceStr)
		} else {
			fmt.Println("Aucune installation enregistrée dans les logs.")
		}
	} else {
		fmt.Printf("\nTotal : %d installation(s) enregistrée(s).\n", total)
	}
}

// openLogs opens UpdateLog.txt in Notepad.
func openLogs() {
	logPath := filepath.Join(logDir, "UpdateLog.txt")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		fmt.Printf("Aucun fichier de log trouvé : %s\n", logPath)
		return
	}
	if err := exec.Command("cmd", "/C", "start", "notepad", logPath).Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Impossible d'ouvrir le fichier de log : %v\n", err)
	}
}

// printExtendedStatus prints service state plus config, log size, and last_check.
func printExtendedStatus() {
	// Service state (from SCM)
	if err := statusService(); err != nil {
		fmt.Fprintln(os.Stderr, "Erreur:", err)
	}

	// Configuration
	fmt.Printf("\nConfiguration (config.json) :\n")
	fmt.Printf("  check_interval_seconds       : %d\n", cfg.CheckIntervalSeconds)
	fmt.Printf("  retry_attempts               : %d\n", cfg.RetryAttempts)
	fmt.Printf("  log_retention_days           : %d\n", cfg.LogRetentionDays)
	fmt.Printf("  max_log_size_mb              : %d\n", cfg.MaxLogSizeMB)
	fmt.Printf("  ps_timeout_minutes           : %d\n", cfg.PSTimeoutMinutes)
	fmt.Printf("  cmd_timeout_seconds          : %d\n", cfg.CmdTimeoutSeconds)
	fmt.Printf("  circuit_breaker_threshold    : %d\n", cfg.CircuitBreakerThreshold)
	fmt.Printf("  circuit_breaker_pause_minutes: %d\n", cfg.CircuitBreakerPauseMinutes)

	// Log file size
	logPath := filepath.Join(logDir, "UpdateLog.txt")
	if info, err := os.Stat(logPath); err == nil {
		fmt.Printf("\nFichier de log :\n  UpdateLog.txt : %.1f KB\n", float64(info.Size())/1024.0)
	} else {
		fmt.Printf("\nFichier de log :\n  UpdateLog.txt : absent\n")
	}

	// Last cycle info from status.json
	statusPath := filepath.Join(logDir, "status.json")
	if data, err := os.ReadFile(statusPath); err == nil {
		var s statusJSON
		if json.Unmarshal(data, &s) == nil {
			fmt.Printf("\nDernière vérification (status.json) :\n")
			fmt.Printf("  last_check         : %s\n", s.LastCheck)
			fmt.Printf("  updates_checked    : %d\n", s.UpdatesChecked)
			fmt.Printf("  updates_installed  : %d\n", s.UpdatesInstalled)
			fmt.Printf("  updates_skipped    : %d\n", s.UpdatesSkipped)
			fmt.Printf("  cycle_errors       : %d\n", s.CycleErrors)
		}
	} else {
		fmt.Printf("\nDernière vérification (status.json) : absent\n")
	}
}

// printShowConfig displays the active configuration (loaded values or defaults).
// If --json is in os.Args, outputs compact JSON instead of human-readable text.
func printShowConfig() {
	// --json flag: output raw JSON
	for _, arg := range os.Args[2:] {
		if arg == "--json" {
			data, err := json.MarshalIndent(cfg, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Erreur JSON : %v\n", err)
				os.Exit(1)
			}
			fmt.Println(string(data))
			return
		}
	}

	exePath, _ := os.Executable()
	cfgPath := filepath.Join(filepath.Dir(exePath), "config.json")
	_, err := os.Stat(cfgPath)
	if err == nil {
		fmt.Printf("Configuration chargée depuis : %s\n\n", cfgPath)
	} else {
		fmt.Println("Aucun config.json trouvé — valeurs par défaut utilisées.")
		fmt.Println()
	}
	fmt.Printf("  check_interval_seconds          : %d\n", cfg.CheckIntervalSeconds)
	fmt.Printf("  retry_attempts                  : %d\n", cfg.RetryAttempts)
	fmt.Printf("  log_retention_days              : %d\n", cfg.LogRetentionDays)
	fmt.Printf("  max_log_size_mb                 : %d\n", cfg.MaxLogSizeMB)
	fmt.Printf("  ps_timeout_minutes              : %d\n", cfg.PSTimeoutMinutes)
	fmt.Printf("  cmd_timeout_seconds             : %d\n", cfg.CmdTimeoutSeconds)
	fmt.Printf("  circuit_breaker_threshold       : %d\n", cfg.CircuitBreakerThreshold)
	fmt.Printf("  circuit_breaker_pause_minutes   : %d\n", cfg.CircuitBreakerPauseMinutes)
	fmt.Printf("  circuit_breaker_reset_minutes   : %d\n", cfg.CircuitBreakerResetMinutes)
	fmt.Printf("  log_level                       : %s\n", cfg.LogLevel)
	fmt.Printf("  notifications_enabled           : %v\n", cfg.NotificationsOn())
	fmt.Printf("  min_free_disk_mb                : %d\n", cfg.MinFreeDiskMB)
	fmt.Printf("  heartbeat_interval_minutes      : %d\n", cfg.HeartbeatIntervalMinutes)
	fmt.Printf("  retry_delay_seconds             : %d\n", cfg.RetryDelaySeconds)
}

// exportConfig writes the active configuration to config.json in the executable directory.
// If the file already exists, --force is required to overwrite it.
func exportConfig() {
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erreur : impossible de localiser l'exécutable : %v\n", err)
		os.Exit(1)
	}
	cfgPath := filepath.Join(filepath.Dir(exePath), "config.json")

	// Check --force flag
	force := false
	for _, arg := range os.Args[2:] {
		if arg == "--force" {
			force = true
		}
	}

	if _, err := os.Stat(cfgPath); err == nil && !force {
		fmt.Fprintf(os.Stderr, "config.json existe déjà. Utilisez --force pour écraser : export-config --force\n")
		os.Exit(1)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Erreur de sérialisation : %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(cfgPath, append(data, '\n'), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "Erreur d'écriture : %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Configuration exportée vers : %s\n", cfgPath)
}

// resetCounters zeroes all atomic counters, clears the install history, and rewrites status.json.
func resetCounters() {
	atomic.StoreInt64(&updatesChecked, 0)
	atomic.StoreInt64(&updatesInstalled, 0)
	atomic.StoreInt64(&updatesSkipped, 0)
	atomic.StoreInt64(&cycleErrors, 0)
	atomic.StoreInt64(&consecutiveErrors, 0)
	lastInstalledMu.Lock()
	lastInstalled = nil
	lastInstalledMu.Unlock()
	writeStatusJSON()
	fmt.Println("Compteurs remis à zéro.")
}

// printReport prints the current counters without resetting them.
func printReport() {
	checked := atomic.LoadInt64(&updatesChecked)
	installed := atomic.LoadInt64(&updatesInstalled)
	skipped := atomic.LoadInt64(&updatesSkipped)
	errors := atomic.LoadInt64(&cycleErrors)
	fmt.Printf("Rapport courant (%s) :\n- Vérifications totales   : %d\n- Mises à jour installées : %d\n- Sans mise à jour        : %d\n- Erreurs                 : %d\n",
		time.Now().Format("2006-01-02 15:04:05"), checked, installed, skipped, errors)
}

func printHelp() {
	fmt.Printf(`WinPiBooster %s — surveillance et installation automatique des mises à jour Windows

Usage:
  WinPiBooster.exe                   Mode interactif (console, Ctrl+C pour quitter)
  WinPiBooster.exe --dry-run         Vérifie les mises à jour disponibles sans les installer
  WinPiBooster.exe check             Alias de --dry-run (même codes de sortie : 0/1/2)
  WinPiBooster.exe install           Installe le service Windows (démarrage automatique)
  WinPiBooster.exe install --start   Installe ET démarre le service en une seule commande
  WinPiBooster.exe start             Démarre le service
  WinPiBooster.exe stop              Arrête le service
  WinPiBooster.exe remove            Désinstalle le service
  WinPiBooster.exe status            Affiche l'état du service
  WinPiBooster.exe clean-logs        Supprime les archives de logs expirées
  WinPiBooster.exe list-logs         Liste tous les fichiers de log avec taille et date
  WinPiBooster.exe tail              Affiche les 20 dernières lignes du log (--lines N pour changer)
  WinPiBooster.exe history           Liste toutes les mises à jour installées (logs courant + archives)
  WinPiBooster.exe history --since DATE  Filtre les installations depuis DATE (format YYYY-MM-DD)
  WinPiBooster.exe logs              Ouvre UpdateLog.txt dans le Bloc-notes
  WinPiBooster.exe report            Affiche les compteurs courants (sans reset)
  WinPiBooster.exe test-notify       Envoie une notification toast de test
  WinPiBooster.exe reset-counters    Remet les compteurs à zéro et réécrit status.json
  WinPiBooster.exe show-config       Affiche la configuration active
  WinPiBooster.exe show-config --json  Affiche la configuration au format JSON
  WinPiBooster.exe export-config    Écrit config.json depuis la configuration active (--force pour écraser)
  WinPiBooster.exe diagnose          Vérifie les prérequis et affiche un rapport de santé
  WinPiBooster.exe version           Affiche la version
  WinPiBooster.exe --version         Alias Unix pour version
  WinPiBooster.exe help              Affiche cette aide

Codes de sortie:
  0   Succès (ou aucune mise à jour disponible en mode --dry-run)
  1   Erreur
  2   Des mises à jour sont disponibles (mode --dry-run uniquement)

Logs:
  UpdateLog.txt dans le répertoire de l'exécutable.
  Rotation quotidienne à minuit, archives conservées %d jours.

Variables d'environnement:
  DEBUG=true    Active les logs verbeux.
`, version, cfg.LogRetentionDays)
}

// ─── Entry point ──────────────────────────────────────────────────────────────

func main() {
	defer func() {
		if r := recover(); r != nil {
			msg := fmt.Sprintf("Exception non catchée — arrêt du script : %v", r)
			if log != nil {
				log.Error(msg)
			} else {
				fmt.Fprintln(os.Stderr, msg)
			}
			showNotification("Erreur fatale", fmt.Sprintf("Exception non catchée : %v", r))
			os.Exit(1)
		}
	}()

	if err := initLogger(); err != nil {
		fmt.Fprintln(os.Stderr, "Logger init failed:", err)
		os.Exit(1)
	}
	if logHook != nil {
		defer logHook.Close()
	}

	// Detect --dry-run and --version flags (may appear at any position)
	dryRun := false
	filteredArgs := os.Args[:1]
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--dry-run":
			dryRun = true
		case "--version":
			fmt.Printf("WinPiBooster %s\n", version)
			os.Exit(0)
		default:
			filteredArgs = append(filteredArgs, arg)
		}
	}

	// Dispatch on first non-flag argument
	cmd := ""
	if len(filteredArgs) > 1 {
		cmd = filteredArgs[1]
	}

	switch cmd {
	case "install":
		if err := installService(); err != nil {
			fmt.Fprintln(os.Stderr, "Erreur:", err)
			os.Exit(1)
		}
		// --start flag: automatically start the service after installation
		for _, arg := range os.Args[2:] {
			if arg == "--start" {
				if err := startService(); err != nil {
					fmt.Fprintln(os.Stderr, "Erreur au démarrage:", err)
					os.Exit(1)
				}
				break
			}
		}
	case "remove":
		if err := removeService(); err != nil {
			fmt.Fprintln(os.Stderr, "Erreur:", err)
			os.Exit(1)
		}
	case "start":
		if err := startService(); err != nil {
			fmt.Fprintln(os.Stderr, "Erreur:", err)
			os.Exit(1)
		}
	case "stop":
		if err := stopService(); err != nil {
			fmt.Fprintln(os.Stderr, "Erreur:", err)
			os.Exit(1)
		}
	case "status":
		printExtendedStatus()
	case "clean-logs":
		cleanOldLogsVerbose(true)
	case "list-logs":
		listLogs()
	case "tail":
		tailLogs()
	case "history":
		historyLogs()
	case "check":
		runDryRun()
	case "logs":
		openLogs()
	case "report":
		printReport()
	case "test-notify":
		testNotify()
	case "reset-counters":
		resetCounters()
	case "show-config":
		printShowConfig()
	case "export-config":
		exportConfig()
	case "diagnose":
		if !runDiagnose() {
			os.Exit(1)
		}
	case "version":
		fmt.Printf("WinPiBooster %s\n", version)
	case "help":
		printHelp()
	case "run":
		// Launched by the SCM — run as a Windows service
		if err := svc.Run(serviceName, &winService{}); err != nil {
			log.Errorf("Service error: %v", err)
			os.Exit(1)
		}
	default:
		if dryRun {
			runDryRun()
		} else {
			runInteractive()
		}
	}
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
