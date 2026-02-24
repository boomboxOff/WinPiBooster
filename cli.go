package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sys/windows"
)

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

	psRaw, psErr := execPS(`$PSVersionTable.PSVersion.ToString()`)
	psVerOK := psErr == nil && strings.TrimSpace(psRaw) != ""
	psVerDetail := ""
	if psVerOK {
		psVerDetail = "v" + strings.TrimSpace(psRaw)
	}
	check("PowerShell", psVerOK, psVerDetail)

	psModOK := isPSWindowsUpdateModuleInstalled()
	psModDetail := ""
	if psModOK {
		if ver, err := execPS(`(Get-Module PSWindowsUpdate -ListAvailable | Select-Object -First 1).Version.ToString()`); err == nil {
			v := strings.TrimSpace(ver)
			if v != "" {
				psModDetail = "v" + v
			}
		}
	}
	check("Module PSWindowsUpdate", psModOK, psModDetail)

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
	threshold := int64(cfg.MinFreeDiskMB)
	if threshold <= 0 {
		threshold = 500
	}
	check("Espace disque libre (C:)", free >= threshold, fmt.Sprintf("%d MB disponibles (seuil : %d MB)", free, threshold))

	fmt.Println()
	if allOK {
		fmt.Println("Tous les prérequis sont satisfaits.")
	} else {
		fmt.Println("Un ou plusieurs prérequis manquants — consultez les détails ci-dessus.")
	}
	return allOK
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
	var totalBytes int64
	count := 0
	for _, p := range entries {
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		found = true
		count++
		totalBytes += info.Size()
		fmt.Printf("  %-40s  %8.1f KB  %s\n",
			filepath.Base(p),
			float64(info.Size())/1024.0,
			info.ModTime().Format("2006-01-02 15:04:05"))
	}
	if !found {
		fmt.Printf("Aucun fichier de log dans %s\n", logDir)
	} else {
		fmt.Printf("─────────────────────────────────\n")
		fmt.Printf("Total : %d fichier(s) — %.1f MB\n", count, float64(totalBytes)/(1024.0*1024.0))
	}
}

// tailLogs prints the last N lines of UpdateLog.txt (default 20).
// Supports --lines N and --grep PATTERN anywhere in os.Args.
// When --grep is set, only matching lines are considered (case-insensitive),
// then --lines N is applied to limit the output.
func tailLogs() {
	n := 20
	grepPattern := ""
	args := os.Args[1:]
	for i, arg := range args {
		if arg == "--lines" && i+1 < len(args) {
			if v, err := strconv.Atoi(args[i+1]); err == nil && v > 0 {
				n = v
			}
		}
		if arg == "--grep" && i+1 < len(args) {
			grepPattern = args[i+1]
		}
	}

	logPath := filepath.Join(logDir, "UpdateLog.txt")
	data, err := os.ReadFile(logPath)
	if err != nil {
		fmt.Printf("Aucun fichier de log trouvé : %s\n", logPath)
		return
	}

	lines := strings.Split(strings.TrimRight(string(data), "\r\n"), "\n")

	if grepPattern != "" {
		pattern := strings.ToLower(grepPattern)
		filtered := lines[:0]
		for _, l := range lines {
			if strings.Contains(strings.ToLower(l), pattern) {
				filtered = append(filtered, l)
			}
		}
		lines = filtered
		if len(lines) == 0 {
			fmt.Printf("Aucune ligne ne correspond au pattern %q.\n", grepPattern)
			return
		}
	}

	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	fmt.Println(strings.Join(lines, "\n"))
}

// historyLogs scans all log files (current + archives) and prints every
// "Installation terminée" line in chronological order.
// Supports --since YYYY-MM-DD and --last N flags.
func historyLogs() {
	args := os.Args[1:]

	// Parse --since YYYY-MM-DD
	sinceStr := ""
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
			sinceStr = ""
		} else {
			sinceTime = t
		}
	}

	// Parse --last N
	lastN := 0
	for i, arg := range args {
		if arg == "--last" && i+1 < len(args) {
			if v, err := strconv.Atoi(args[i+1]); err == nil && v > 0 {
				lastN = v
			}
		}
	}

	current := filepath.Join(logDir, "UpdateLog.txt")
	archives, _ := filepath.Glob(filepath.Join(logDir, "UpdateLog_*.txt"))

	// Build ordered file list: archives first (sorted), then current log
	sort.Strings(archives)
	files := append(archives, current)

	// Collect all matching lines first
	var matchedLines []string
	distinctKBs := make(map[string]bool)
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
				matchedLines = append(matchedLines, strings.TrimRight(line, "\r"))
				if idx := strings.Index(line, "Installation terminée :"); idx >= 0 {
					kbPart := strings.TrimSpace(line[idx+len("Installation terminée :"):])
					for _, kb := range strings.Split(kbPart, ",") {
						kb = strings.TrimSpace(kb)
						if kb != "" {
							distinctKBs[kb] = true
						}
					}
				}
			}
		}
	}

	// Apply --last N
	if lastN > 0 && len(matchedLines) > lastN {
		matchedLines = matchedLines[len(matchedLines)-lastN:]
	}

	if len(matchedLines) == 0 {
		if sinceStr != "" {
			fmt.Printf("Aucune installation enregistrée depuis le %s.\n", sinceStr)
		} else {
			fmt.Println("Aucune installation enregistrée dans les logs.")
		}
		return
	}
	for _, l := range matchedLines {
		fmt.Println(l)
	}
	fmt.Printf("\nTotal : %d installation(s) enregistrée(s). (%d KB distincts)\n", len(matchedLines), len(distinctKBs))
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
			if s.Version != "" {
				fmt.Printf("  version            : %s\n", s.Version)
			}
			fmt.Printf("  last_check         : %s\n", s.LastCheck)
			if s.NextCheck != "" {
				if nextT, err2 := time.Parse(time.RFC3339, s.NextCheck); err2 == nil {
					rem := time.Until(nextT).Round(time.Second)
					var remStr string
					if rem > 0 {
						remStr = fmt.Sprintf("dans %s", rem)
					} else {
						remStr = "passé"
					}
					fmt.Printf("  next_check         : %s (%s)\n", s.NextCheck, remStr)
				}
			}
			fmt.Printf("  updates_checked    : %d\n", s.UpdatesChecked)
			fmt.Printf("  updates_installed  : %d\n", s.UpdatesInstalled)
			fmt.Printf("  updates_skipped    : %d\n", s.UpdatesSkipped)
			fmt.Printf("  cycle_errors       : %d\n", s.CycleErrors)
			if s.LastError != "" {
				fmt.Printf("  last_error         : %s\n", s.LastError)
			}
		}
	} else {
		fmt.Printf("\nDernière vérification (status.json) : absent\n")
	}
}

// printShowConfig displays the active configuration (loaded values or defaults).
// If --json is in os.Args, outputs compact JSON instead of human-readable text.
// If --diff is in os.Args, shows only values that differ from defaults.
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

	// --diff flag: show only values that differ from defaults
	diffMode := false
	for _, arg := range os.Args[2:] {
		if arg == "--diff" {
			diffMode = true
		}
	}

	d := defaults()

	type field struct {
		name    string
		val     string
		isDefault bool
	}
	fields := []field{
		{"check_interval_seconds     ", fmt.Sprintf("%d", cfg.CheckIntervalSeconds), cfg.CheckIntervalSeconds == d.CheckIntervalSeconds},
		{"retry_attempts             ", fmt.Sprintf("%d", cfg.RetryAttempts), cfg.RetryAttempts == d.RetryAttempts},
		{"log_retention_days         ", fmt.Sprintf("%d", cfg.LogRetentionDays), cfg.LogRetentionDays == d.LogRetentionDays},
		{"max_log_size_mb            ", fmt.Sprintf("%d", cfg.MaxLogSizeMB), cfg.MaxLogSizeMB == d.MaxLogSizeMB},
		{"ps_timeout_minutes         ", fmt.Sprintf("%d", cfg.PSTimeoutMinutes), cfg.PSTimeoutMinutes == d.PSTimeoutMinutes},
		{"cmd_timeout_seconds        ", fmt.Sprintf("%d", cfg.CmdTimeoutSeconds), cfg.CmdTimeoutSeconds == d.CmdTimeoutSeconds},
		{"log_level                  ", cfg.LogLevel, cfg.LogLevel == d.LogLevel},
		{"notifications_enabled      ", fmt.Sprintf("%v", cfg.NotificationsOn()), cfg.NotificationsOn() == d.NotificationsOn()},
		{"min_free_disk_mb           ", fmt.Sprintf("%d", cfg.MinFreeDiskMB), cfg.MinFreeDiskMB == d.MinFreeDiskMB},
		{"heartbeat_interval_minutes ", fmt.Sprintf("%d", cfg.HeartbeatIntervalMinutes), cfg.HeartbeatIntervalMinutes == d.HeartbeatIntervalMinutes},
		{"retry_delay_seconds        ", fmt.Sprintf("%d", cfg.RetryDelaySeconds), cfg.RetryDelaySeconds == d.RetryDelaySeconds},
	}

	if diffMode {
		changed := 0
		for _, f := range fields {
			if !f.isDefault {
				fmt.Printf("  %s: %s\n", f.name, f.val)
				changed++
			}
		}
		if changed == 0 {
			fmt.Println("Aucune valeur surchargée — configuration identique aux défauts.")
		}
		return
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
	mark := func(isDefault bool) string {
		if isDefault {
			return "  (défaut)"
		}
		return ""
	}
	for _, f := range fields {
		fmt.Printf("  %s: %s%s\n", f.name, f.val, mark(f.isDefault))
	}
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
	lastInstalledMu.Lock()
	lastInstalled = nil
	lastInstalledMu.Unlock()
	writeStatusJSON()
	fmt.Println("Compteurs remis à zéro.")
}

// printReport prints the current counters without resetting them.
// If status.json is present, also shows uptime.
func printReport() {
	checked := atomic.LoadInt64(&updatesChecked)
	installed := atomic.LoadInt64(&updatesInstalled)
	skipped := atomic.LoadInt64(&updatesSkipped)
	errors := atomic.LoadInt64(&cycleErrors)
	fmt.Printf("Rapport courant (%s) :\n", time.Now().Format("2006-01-02 15:04:05"))

	statusPath := filepath.Join(logDir, "status.json")
	if data, err := os.ReadFile(statusPath); err == nil {
		var s statusJSON
		if json.Unmarshal(data, &s) == nil && s.UptimeSeconds > 0 {
			fmt.Printf("- Uptime                  : %s\n", formatUptime(time.Duration(s.UptimeSeconds)*time.Second))
		}
	}

	fmt.Printf("- Vérifications totales   : %d\n", checked)
	fmt.Printf("- Mises à jour installées : %d\n", installed)
	fmt.Printf("- Sans mise à jour        : %d\n", skipped)
	fmt.Printf("- Erreurs                 : %d\n", errors)
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
