package main

import (
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
	"gopkg.in/toast.v1"
)

// ─── Globals ──────────────────────────────────────────────────────────────────

var (
	log           *logrus.Logger
	logHook       *fileHook
	logDir        string // directory of the executable

	// Counters (atomic)
	updatesChecked   int64
	updatesInstalled int64
	updatesSkipped   int64

	// Prevent concurrent update cycles
	cycleMu sync.Mutex

	// Cached flag: PSWindowsUpdate module ready
	psModuleReady bool
	psModuleMu    sync.Mutex
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

// execCommand runs a shell command through cmd /C and returns trimmed stdout.
func execCommand(cmd string) (string, error) {
	out, err := exec.Command("cmd", "/C", cmd).Output()
	if err != nil {
		// Try to get stderr for better error messages
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// execPS runs a PowerShell command with UTF-8 encoding enforced.
func execPS(psCmd string) (string, error) {
	full := fmt.Sprintf(
		`[Console]::OutputEncoding = [System.Text.Encoding]::UTF8; chcp 65001 | Out-Null; %s`,
		psCmd,
	)
	out, err := exec.Command(
		"powershell.exe", "-NoProfile", "-Command", full,
	).Output()
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
	cutoff := time.Now().AddDate(0, 0, -30)
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

func generateDailyReport() {
	checked := atomic.SwapInt64(&updatesChecked, 0)
	installed := atomic.SwapInt64(&updatesInstalled, 0)
	skipped := atomic.SwapInt64(&updatesSkipped, 0)

	report := fmt.Sprintf("Rapport quotidien :\n- Vérifications totales : %d\n- Mises à jour installées : %d\n- Vérifications sans mise à jour : %d",
		checked, installed, skipped)
	log.Info(report)
	showNotification("Rapport quotidien", report)
}

func heartbeat() {
	log.Info(strings.Repeat("─", 62))
	log.Info("Script actif — surveillance des mises à jour Windows en cours.")
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

// ─── Main cycle ───────────────────────────────────────────────────────────────

func runCycle() {
	// TryLock: skip cycle if a previous one is still running
	if !cycleMu.TryLock() {
		log.Debug("Cycle précédent toujours en cours, passage ignoré.")
		return
	}
	defer cycleMu.Unlock()

	log.Debug("Lancement du processus de mise à jour Windows...")

	if err := installPSWindowsUpdateModule(); err != nil {
		log.Errorf("Erreur globale du processus de mise à jour : %v", err)
		showNotification("Erreur", "Erreur globale du processus de mise à jour.")
		return
	}

	if err := ensureWindowsUpdateServiceRunning(); err != nil {
		log.Errorf("Erreur globale du processus de mise à jour : %v", err)
		showNotification("Erreur", "Erreur globale du processus de mise à jour.")
		return
	}

	updates, err := checkAvailableUpdates()
	if err != nil {
		// Error already logged inside checkAvailableUpdates
		return
	}

	if len(updates) > 0 {
		if err := installUpdates(updates); err != nil {
			log.Errorf("Erreur globale du processus de mise à jour : %v", err)
			showNotification("Erreur", "Erreur globale du processus de mise à jour.")
		}
	}

	log.Debug("Processus terminé.")
}

// ─── Entry point ──────────────────────────────────────────────────────────────

func main() {
	// Recover from panics
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

	// Resolve executable directory (equivalent of Node's __dirname)
	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Cannot determine executable path:", err)
		os.Exit(1)
	}
	logDir = filepath.Dir(exePath)

	// Initialise logger
	log, logHook, err = setupLogger()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Logger init failed:", err)
		os.Exit(1)
	}
	if logHook != nil {
		defer logHook.Close()
	}

	// Check admin rights
	if err := checkAdminRights(); err != nil {
		log.Error("Le script doit être exécuté en tant qu'administrateur. Relancez via WinPiBooster.bat en tant qu'administrateur.")
		showNotification("Erreur", "Droits administrateur requis. Relancez en tant qu'administrateur.")
		os.Exit(1)
	}

	// Archive previous log on startup
	archiveOldLogs()

	// Initial heartbeat
	heartbeat()

	// Hourly heartbeat
	heartbeatTicker := time.NewTicker(time.Hour)
	go func() {
		for range heartbeatTicker.C {
			heartbeat()
		}
	}()

	// Daily report at midnight
	scheduleDailyReport()

	// First update cycle immediately
	go runCycle()

	// Update cycle every 60 seconds (intentional interval for Pi node)
	cycleTicker := time.NewTicker(60 * time.Second)
	go func() {
		for range cycleTicker.C {
			log.Debug("Début d'un nouveau cycle de vérification des mises à jour.")
			go runCycle()
		}
	}()

	// Graceful shutdown on SIGINT / SIGTERM
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	sig := <-sigs
	log.Infof("Arrêt du script demandé (%s).", sig)
	heartbeatTicker.Stop()
	cycleTicker.Stop()
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
