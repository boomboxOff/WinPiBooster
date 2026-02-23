package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
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
	cycleTicker := time.NewTicker(60 * time.Second)

	archiveOldLogs()
	heartbeat()
	scheduleDailyReport()
	go runCycle()

	go func() {
		for range heartbeatTicker.C {
			heartbeat()
		}
	}()
	go func() {
		for range cycleTicker.C {
			log.Debug("Début d'un nouveau cycle de vérification des mises à jour.")
			go runCycle()
		}
	}()

	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

loop:
	for c := range req {
		switch c.Cmd {
		case svc.Stop, svc.Shutdown:
			log.Infof("Arrêt du service demandé (%v).", c.Cmd)
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
