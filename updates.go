package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"time"
)

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
		return err
	}
	if strings.Contains(strings.ToLower(result), "error") {
		msg := "Erreur détectée pendant l'installation : Conflit potentiel avec les politiques de sécurité ou les permissions administratives."
		log.Error(msg)
		return fmt.Errorf("%s", msg)
	}

	log.Info("Module PSWindowsUpdate installé avec succès.")
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

	if err := retryBackoff("installPSWindowsUpdateModule", retryAttempts(), defaultBackoff(), installPSWindowsUpdateModule); err != nil {
		atomic.AddInt64(&cycleErrors, 1)
		setCycleError(err.Error())
		log.Errorf("Erreur globale du processus de mise à jour : %v", err)
		return
	}

	if err := retryBackoff("ensureWindowsUpdateServiceRunning", retryAttempts(), defaultBackoff(), ensureWindowsUpdateServiceRunning); err != nil {
		atomic.AddInt64(&cycleErrors, 1)
		setCycleError(err.Error())
		log.Errorf("Erreur globale du processus de mise à jour : %v", err)
		return
	}

	updates, err := checkAvailableUpdates()
	if err != nil {
		atomic.AddInt64(&cycleErrors, 1)
		setCycleError(err.Error())
		// Error already logged inside checkAvailableUpdates
		return
	}

	if len(updates) > 0 {
		if isRebootPending() {
			log.Warn("Redémarrage en attente détecté — installation des mises à jour reportée au prochain cycle.")
			return
		}
		if minFree := int64(cfg.MinFreeDiskMB); minFree > 0 {
			if free := freeDiskMB(); free < minFree {
				log.Warnf("Espace disque insuffisant (%d MB libres, minimum %d MB) — installation ignorée.", free, minFree)
				atomic.AddInt64(&updatesSkipped, int64(len(updates)))
				return
			}
		}
		err := retryBackoff("installUpdates", retryAttempts(), defaultBackoff(), func() error {
			return installUpdates(updates)
		})
		if err != nil {
			atomic.AddInt64(&cycleErrors, 1)
			setCycleError(err.Error())
			log.Errorf("Erreur globale du processus de mise à jour : %v", err)
			return
		}
	}

	clearCycleError()
	writeStatusJSON()
	log.Debug("Processus terminé.")
}
