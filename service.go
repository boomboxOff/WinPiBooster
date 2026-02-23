package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const serviceName = "WinPiBooster"
const serviceDisplayName = "WinPiBooster — Windows Update Monitor"
const serviceDescription = "Surveille et installe automatiquement les mises à jour Windows pour le nœud Pi Network."

// ─── Service handler ──────────────────────────────────────────────────────────

// winService implements svc.Handler.
type winService struct{}

func (ws *winService) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}

	// Start the update loop in background
	heartbeatTicker := time.NewTicker(time.Hour)
	cycleTicker := time.NewTicker(cfg.CheckInterval())

	showNotification("WinPiBooster démarré", "Surveillance des mises à jour Windows active.")
	archiveOldLogs()
	heartbeat()
	scheduleDailyReport()
	go runCycle()

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

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

loop:
	for c := range req {
		switch c.Cmd {
		case svc.Stop, svc.Shutdown:
			log.Infof("Arrêt du service demandé (%v).", c.Cmd)
			shutdownCancel()
			showNotification("WinPiBooster arrêté", "La surveillance des mises à jour Windows est inactive.")
			break loop
		case svc.Interrogate:
			status <- c.CurrentStatus
		}
	}

	heartbeatTicker.Stop()
	cycleTicker.Stop()
	status <- svc.Status{State: svc.StopPending}
	return false, 0
}

// ─── SCM helpers ──────────────────────────────────────────────────────────────

func installService() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot find executable: %w", err)
	}
	// Resolve symlinks so the SCM gets the real path
	exePath, err = filepath.EvalSymlinks(exePath)
	if err != nil {
		return fmt.Errorf("cannot resolve executable path: %w", err)
	}

	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("cannot connect to SCM: %w", err)
	}
	defer m.Disconnect()

	// Check if already exists
	s, err := m.OpenService(serviceName)
	if err == nil {
		s.Close()
		return fmt.Errorf("service %q already exists", serviceName)
	}

	s, err = m.CreateService(serviceName, exePath, mgr.Config{
		DisplayName:      serviceDisplayName,
		Description:      serviceDescription,
		StartType:        mgr.StartAutomatic,
		ServiceStartName: "LocalSystem",
	}, "run")
	if err != nil {
		return fmt.Errorf("cannot create service: %w", err)
	}
	defer s.Close()

	// Register Event Log source
	if err := eventlog.InstallAsEventCreate(serviceName, eventlog.Error|eventlog.Warning|eventlog.Info); err != nil {
		fmt.Printf("Avertissement : impossible d'enregistrer la source Event Log : %v\n", err)
	}

	fmt.Printf("Service %q installé avec succès.\n", serviceName)
	fmt.Println("Démarrez-le avec : WinPiBooster.exe start")
	return nil
}

func removeService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("cannot connect to SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q not found: %w", serviceName, err)
	}
	defer s.Close()

	if err := s.Delete(); err != nil {
		return fmt.Errorf("cannot delete service: %w", err)
	}

	// Remove Event Log source
	if err := eventlog.Remove(serviceName); err != nil {
		fmt.Printf("Avertissement : impossible de supprimer la source Event Log : %v\n", err)
	}

	fmt.Printf("Service %q supprimé.\n", serviceName)
	return nil
}

func startService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("cannot connect to SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q not found: %w", serviceName, err)
	}
	defer s.Close()

	if err := s.Start(); err != nil {
		return fmt.Errorf("cannot start service: %w", err)
	}
	fmt.Printf("Service %q démarré.\n", serviceName)
	return nil
}

func statusService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("cannot connect to SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		fmt.Printf("Service %q : non installé\n", serviceName)
		return nil
	}
	defer s.Close()

	status, err := s.Query()
	if err != nil {
		return fmt.Errorf("cannot query service: %w", err)
	}

	states := map[svc.State]string{
		svc.Stopped:         "Stopped",
		svc.StartPending:    "StartPending",
		svc.StopPending:     "StopPending",
		svc.Running:         "Running",
		svc.ContinuePending: "ContinuePending",
		svc.PausePending:    "PausePending",
		svc.Paused:          "Paused",
	}
	state, ok := states[status.State]
	if !ok {
		state = fmt.Sprintf("Unknown(%d)", status.State)
	}
	fmt.Printf("Service %q : %s\n", serviceName, state)
	return nil
}

func stopService() error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("cannot connect to SCM: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err != nil {
		return fmt.Errorf("service %q not found: %w", serviceName, err)
	}
	defer s.Close()

	status, err := s.Control(svc.Stop)
	if err != nil {
		return fmt.Errorf("cannot stop service: %w", err)
	}
	timeout := time.Now().Add(10 * time.Second)
	for status.State != svc.Stopped {
		if time.Now().After(timeout) {
			return fmt.Errorf("timeout waiting for service to stop")
		}
		time.Sleep(300 * time.Millisecond)
		status, err = s.Query()
		if err != nil {
			return fmt.Errorf("cannot query service status: %w", err)
		}
	}
	fmt.Printf("Service %q arrêté.\n", serviceName)
	return nil
}
