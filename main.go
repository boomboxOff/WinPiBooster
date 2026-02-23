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
	"golang.org/x/sys/windows/svc"
	"gopkg.in/toast.v1"
)

// version is set at build time via -ldflags "-X main.version=vX.Y.Z"
var version = "dev"

// Timeouts for child process execution
const (
	cmdTimeout = 5 * time.Minute  // sc, net session, sc start
	psTimeout  = 10 * time.Minute // PowerShell (Get/Install-WindowsUpdate can be slow)
)

// ─── Globals ──────────────────────────────────────────────────────────────────

var (
	log           *logrus.Logger
	logHook       *fileHook
	logDir        string // directory of the executable
	cfg           Config

	// Counters (atomic)
	updatesChecked   int64
	updatesInstalled int64
	updatesSkipped   int64
	cycleErrors      int64

	// Prevent concurrent update cycles
	cycleMu sync.Mutex

	// Cached flag: PSWindowsUpdate module ready
	psModuleReady bool
	psModuleMu    sync.Mutex

	// Global shutdown context — cancelled on SIGINT/SIGTERM or service stop.
	shutdownCtx, shutdownCancel = context.WithCancel(context.Background())
)

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
	ctx, cancel := context.WithTimeout(shutdownCtx, cmdTimeout)
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
	ctx, cancel := context.WithTimeout(shutdownCtx, psTimeout)
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
func showNotification(title, message string) {
	n := toast.Notification{
		AppID:   "WinPiBooster",
		Title:   title,
		Message: message,
		Audio:   toast.Default,
	}
	if err := n.Push(); err != nil {
		log.Debugf("Notification non envoyée : %v", err)
	}
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
	days := cfg.LogRetentionDays
	if days <= 0 {
		days = 30
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	entries, err := os.ReadDir(logDir)
	if err != nil {
		log.Warnf("cleanOldLogs: cannot read dir: %v", err)
		return
	}
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
				log.Warnf("cleanOldLogs: cannot remove %s: %v", name, err)
			} else {
				log.Debugf("Ancien journal supprimé : %s", name)
			}
		}
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

	report := buildDailyReport(checked, installed, skipped, errors)
	log.Info(report)
	showNotification("Rapport quotidien", report)
}

func heartbeat() {
	log.Info(strings.Repeat("─", 62))
	log.Infof("WinPiBooster %s — surveillance des mises à jour Windows en cours.", version)
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
			generateDailyReport()
			cleanOldLogs()
			timer.Reset(durationUntilMidnight())
		}
	}()
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

var defaultBackoff = []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}

func retryAttempts() int {
	if cfg.RetryAttempts > 0 {
		return cfg.RetryAttempts
	}
	return 3
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

	log.Debug("Lancement du processus de mise à jour Windows...")

	if err := retryBackoff("installPSWindowsUpdateModule", retryAttempts(), defaultBackoff, installPSWindowsUpdateModule); err != nil {
		atomic.AddInt64(&cycleErrors, 1)
		log.Errorf("Erreur globale du processus de mise à jour : %v", err)
		showNotification("Erreur", "Erreur globale du processus de mise à jour.")
		return
	}

	if err := retryBackoff("ensureWindowsUpdateServiceRunning", retryAttempts(), defaultBackoff, ensureWindowsUpdateServiceRunning); err != nil {
		atomic.AddInt64(&cycleErrors, 1)
		log.Errorf("Erreur globale du processus de mise à jour : %v", err)
		showNotification("Erreur", "Erreur globale du processus de mise à jour.")
		return
	}

	updates, err := checkAvailableUpdates()
	if err != nil {
		atomic.AddInt64(&cycleErrors, 1)
		// Error already logged inside checkAvailableUpdates
		return
	}

	if len(updates) > 0 {
		err := retryBackoff("installUpdates", retryAttempts(), defaultBackoff, func() error {
			return installUpdates(updates)
		})
		if err != nil {
			atomic.AddInt64(&cycleErrors, 1)
			log.Errorf("Erreur globale du processus de mise à jour : %v", err)
			showNotification("Erreur", "Erreur globale du processus de mise à jour.")
		}
	}

	log.Debug("Processus terminé.")
}

// ─── Startup helpers ──────────────────────────────────────────────────────────

// initLogger initialises the logger and sets logDir from the executable path.
func initLogger() error {
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

// runInteractive runs the update loop in console mode (SIGINT/SIGTERM aware).
func runInteractive() {
	if err := checkAdminRights(); err != nil {
		log.Error("Le script doit être exécuté en tant qu'administrateur. Relancez via WinPiBooster.bat en tant qu'administrateur.")
		showNotification("Erreur", "Droits administrateur requis. Relancez en tant qu'administrateur.")
		os.Exit(1)
	}

	archiveOldLogs()
	heartbeat()

	heartbeatTicker := time.NewTicker(time.Hour)
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

// ─── Help ─────────────────────────────────────────────────────────────────────

func printHelp() {
	fmt.Printf(`WinPiBooster %s — surveillance et installation automatique des mises à jour Windows

Usage:
  WinPiBooster.exe                   Mode interactif (console, Ctrl+C pour quitter)
  WinPiBooster.exe install           Installe le service Windows (démarrage automatique)
  WinPiBooster.exe start             Démarre le service
  WinPiBooster.exe stop              Arrête le service
  WinPiBooster.exe remove            Désinstalle le service
  WinPiBooster.exe status            Affiche l'état du service
  WinPiBooster.exe version           Affiche la version
  WinPiBooster.exe help              Affiche cette aide

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

	// Dispatch on first argument
	cmd := ""
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "install":
		if err := installService(); err != nil {
			fmt.Fprintln(os.Stderr, "Erreur:", err)
			os.Exit(1)
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
		if err := statusService(); err != nil {
			fmt.Fprintln(os.Stderr, "Erreur:", err)
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
		// No argument (or unknown) — interactive console mode
		runInteractive()
	}
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
