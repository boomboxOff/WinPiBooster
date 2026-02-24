package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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
	log     *logrus.Logger
	logHook *fileHook
	logDir  string // directory of the executable
	cfg     Config

	// Counters (atomic) — reset by daily report
	updatesChecked   int64
	updatesInstalled int64
	updatesSkipped   int64
	cycleErrors      int64

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
	Title          string      `json:"Title"`
	KBArticleIDs   interface{} `json:"KBArticleIDs"` // may be string or []string
	Size           interface{} `json:"Size"`
	PSComputerName string      `json:"PSComputerName"`
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

// deaccent replaces French accented characters with their ASCII equivalents.
// gopkg.in/toast.v1 reads its temp XML file without -Encoding UTF8, so UTF-8
// multi-byte sequences are misread as ANSI (cp1252) on French Windows.
var deaccentReplacer = strings.NewReplacer(
	"à", "a", "â", "a", "á", "a",
	"è", "e", "é", "e", "ê", "e", "ë", "e",
	"î", "i", "ï", "i", "í", "i",
	"ô", "o", "ö", "o", "ó", "o",
	"ù", "u", "û", "u", "ü", "u", "ú", "u",
	"ç", "c",
	"ñ", "n",
	"À", "A", "Â", "A", "Á", "A",
	"È", "E", "É", "E", "Ê", "E", "Ë", "E",
	"Î", "I", "Ï", "I", "Í", "I",
	"Ô", "O", "Ö", "O", "Ó", "O",
	"Ù", "U", "Û", "U", "Ü", "U", "Ú", "U",
	"Ç", "C",
	"Ñ", "N",
	"æ", "ae", "Æ", "AE",
	"œ", "oe", "Œ", "OE",
)

func deaccent(s string) string { return deaccentReplacer.Replace(s) }

// showNotification sends a Windows toast notification (best-effort).
// Does nothing if notifications are disabled in config.
// Accented characters are transliterated to ASCII to work around a toast.v1
// encoding bug (Get-Content without -Encoding UTF8).
func showNotification(title, message string) {
	if !cfg.NotificationsOn() {
		return
	}
	n := toast.Notification{
		AppID:   "WinPiBooster",
		Title:   deaccent(title),
		Message: deaccent(message),
		Audio:   toast.Default,
	}
	if err := n.Push(); err != nil {
		if log != nil {
			log.Debugf("Notification non envoyée : %v", err)
		}
	}
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
