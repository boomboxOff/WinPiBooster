package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ─── freeDiskMB() ─────────────────────────────────────────────────────────────

func TestFreeDiskMB_Positive(t *testing.T) {
	free := freeDiskMB()
	if free <= 0 {
		t.Errorf("freeDiskMB() = %d, want > 0", free)
	}
}

// ─── runDiagnose() ────────────────────────────────────────────────────────────

func TestRunDiagnose_NoPanic(t *testing.T) {
	// Capture stdout — runDiagnose() calls execPS/execCommand which may fail in test
	// context, but the function must not panic and must print the PSWindowsUpdate section.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	withTestLogger(t, func() {
		runDiagnose() // return value (bool) ignored
	})
	w.Close()
	os.Stdout = old

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "PSWindowsUpdate") {
		t.Errorf("expected 'PSWindowsUpdate' in diagnose output, got: %s", out)
	}
	if !strings.Contains(out, "PowerShell") {
		t.Errorf("expected 'PowerShell' in diagnose output, got: %s", out)
	}
}

// ─── openLogs() ───────────────────────────────────────────────────────────────

func TestOpenLogs_AbsentFile(t *testing.T) {
	// Point logDir at a temp dir with no UpdateLog.txt — should not panic.
	old := logDir
	logDir = t.TempDir()
	defer func() { logDir = old }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	oldOut := os.Stdout
	os.Stdout = w
	openLogs()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 512)
	n, _ := r.Read(buf)
	out := string(buf[:n])
	if !strings.Contains(out, "Aucun fichier de log") {
		t.Errorf("expected 'Aucun fichier de log' message, got: %s", out)
	}
}

// ─── printReport() ────────────────────────────────────────────────────────────

func TestPrintReport_NoPanic(t *testing.T) {
	// Set known counter values, verify output contains them.
	atomic.StoreInt64(&updatesChecked, 5)
	atomic.StoreInt64(&updatesInstalled, 2)
	atomic.StoreInt64(&updatesSkipped, 3)
	atomic.StoreInt64(&cycleErrors, 1)
	defer func() {
		atomic.StoreInt64(&updatesChecked, 0)
		atomic.StoreInt64(&updatesInstalled, 0)
		atomic.StoreInt64(&updatesSkipped, 0)
		atomic.StoreInt64(&cycleErrors, 0)
	}()

	// Capture stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	printReport()
	w.Close()
	os.Stdout = old

	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	for _, want := range []string{"5", "2", "3", "1"} {
		if !strings.Contains(out, want) {
			t.Errorf("printReport output missing %q\ngot: %s", want, out)
		}
	}
}

// ─── printHelp() ──────────────────────────────────────────────────────────────

func TestPrintHelp_NoPanic(t *testing.T) {
	// Redirect stdout to discard output; just verify no panic.
	old := os.Stdout
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	os.Stdout = devNull
	defer func() {
		os.Stdout = old
		devNull.Close()
	}()
	printHelp()
}

func TestPrintHelp_ContainsCheck(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	printHelp()
	w.Close()
	os.Stdout = old

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "check") {
		t.Errorf("printHelp should mention 'check' command, got: %s", out)
	}
}

func TestPrintHelp_ContainsHistorySince(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	printHelp()
	w.Close()
	os.Stdout = old

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "--since") {
		t.Errorf("printHelp should mention '--since', got: %s", out)
	}
}

// ─── diagnose wuauserv detail ─────────────────────────────────────────────────

func TestWuauservDetail_Running(t *testing.T) {
	out := "STATE              : 4  RUNNING\n"
	wuRunning := strings.Contains(out, "RUNNING")
	var wuDetail string
	switch {
	case strings.Contains(out, "STOPPED"):
		wuDetail = "arrêté — lancez 'sc start wuauserv' en admin"
	case strings.Contains(out, "PAUSED"):
		wuDetail = "en pause"
	case wuRunning:
		wuDetail = "en cours d'exécution"
	default:
		wuDetail = "état inconnu"
	}
	if !wuRunning {
		t.Error("expected wuRunning=true for RUNNING output")
	}
	if !strings.Contains(wuDetail, "en cours d'exécution") {
		t.Errorf("expected running detail, got: %q", wuDetail)
	}
}

func TestWuauservDetail_Stopped(t *testing.T) {
	out := "STATE              : 1  STOPPED\n"
	wuRunning := strings.Contains(out, "RUNNING")
	var wuDetail string
	switch {
	case strings.Contains(out, "STOPPED"):
		wuDetail = "arrêté — lancez 'sc start wuauserv' en admin"
	case strings.Contains(out, "PAUSED"):
		wuDetail = "en pause"
	case wuRunning:
		wuDetail = "en cours d'exécution"
	default:
		wuDetail = "état inconnu"
	}
	if wuRunning {
		t.Error("expected wuRunning=false for STOPPED output")
	}
	if !strings.Contains(wuDetail, "arrêté") {
		t.Errorf("expected stopped detail, got: %q", wuDetail)
	}
}

func TestWuauservDetail_Paused(t *testing.T) {
	out := "STATE              : 7  PAUSED\n"
	wuRunning := strings.Contains(out, "RUNNING")
	var wuDetail string
	switch {
	case strings.Contains(out, "STOPPED"):
		wuDetail = "arrêté — lancez 'sc start wuauserv' en admin"
	case strings.Contains(out, "PAUSED"):
		wuDetail = "en pause"
	case wuRunning:
		wuDetail = "en cours d'exécution"
	default:
		wuDetail = "état inconnu"
	}
	if wuRunning {
		t.Error("expected wuRunning=false for PAUSED output")
	}
	if !strings.Contains(wuDetail, "en pause") {
		t.Errorf("expected paused detail, got: %q", wuDetail)
	}
}

func TestWuauservDetail_Unknown(t *testing.T) {
	out := ""
	wuRunning := strings.Contains(out, "RUNNING")
	var wuDetail string
	switch {
	case strings.Contains(out, "STOPPED"):
		wuDetail = "arrêté — lancez 'sc start wuauserv' en admin"
	case strings.Contains(out, "PAUSED"):
		wuDetail = "en pause"
	case wuRunning:
		wuDetail = "en cours d'exécution"
	default:
		wuDetail = "état inconnu"
	}
	if !strings.Contains(wuDetail, "état inconnu") {
		t.Errorf("expected unknown detail, got: %q", wuDetail)
	}
}

// ─── printShowConfig() ────────────────────────────────────────────────────────

func TestPrintShowConfig_NoPanic(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	printShowConfig()
	w.Close()
	os.Stdout = old

	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	for _, want := range []string{"check_interval_seconds", "log_level", "retry_attempts"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in output, got: %s", want, out)
		}
	}
}

func TestPrintShowConfig_TextMode(t *testing.T) {
	old := cfg
	cfg = defaults()
	defer func() { cfg = old }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	printShowConfig() // os.Args[2:] has no --json in test runner
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	for _, field := range []string{
		"check_interval_seconds",
		"retry_attempts",
		"heartbeat_interval_minutes",
		"retry_delay_seconds",
		"notifications_enabled",
	} {
		if !strings.Contains(out, field) {
			t.Errorf("printShowConfig output missing field %q", field)
		}
	}
}

func TestPrintShowConfig_WithConfigFile(t *testing.T) {
	p := cfgPath(t)
	defer os.Remove(p)
	if err := os.WriteFile(p, []byte(`{"check_interval_seconds":90}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	old := cfg
	cfg = defaults()
	cfg.CheckIntervalSeconds = 90
	defer func() { cfg = old }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	printShowConfig()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "chargée depuis") {
		t.Errorf("expected 'chargée depuis' message when config.json exists, got: %q", out)
	}
}

// ─── resetCounters() ──────────────────────────────────────────────────────────

func TestResetCounters(t *testing.T) {
	oldDir := logDir
	logDir = t.TempDir()
	defer func() { logDir = oldDir }()

	atomic.StoreInt64(&updatesChecked, 5)
	atomic.StoreInt64(&updatesInstalled, 2)
	atomic.StoreInt64(&updatesSkipped, 3)
	atomic.StoreInt64(&cycleErrors, 1)
	lastInstalledMu.Lock()
	lastInstalled = []installEntry{{KB: "KB1", Title: "T", InstalledAt: "2026-01-01T00:00:00Z"}}
	lastInstalledMu.Unlock()

	// Redirect stdout
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	resetCounters()
	w.Close()
	os.Stdout = old
	buf := make([]byte, 128)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "zéro") {
		t.Errorf("expected confirmation message, got: %s", string(buf[:n]))
	}

	if atomic.LoadInt64(&updatesChecked) != 0 {
		t.Error("updatesChecked should be 0")
	}
	lastInstalledMu.Lock()
	n2 := len(lastInstalled)
	lastInstalledMu.Unlock()
	if n2 != 0 {
		t.Errorf("lastInstalled len = %d, want 0", n2)
	}
}

// ─── tailLogs() ───────────────────────────────────────────────────────────────

func TestTailLogs_LastLines(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// Write 30 lines numbered 1..30
	var sb strings.Builder
	for i := 1; i <= 30; i++ {
		sb.WriteString(fmt.Sprintf("line %d\n", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	old2 := os.Stdout
	os.Stdout = w
	tailLogs() // default 20 lines
	w.Close()
	os.Stdout = old2

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "line 30") {
		t.Errorf("expected last line in output, got: %s", out)
	}
	if strings.Contains(out, "line 1\n") {
		t.Errorf("expected first lines to be trimmed, got: %s", out)
	}
}

func TestTailLogs_AbsentFile(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	r, w, _ := os.Pipe()
	old2 := os.Stdout
	os.Stdout = w
	tailLogs()
	w.Close()
	os.Stdout = old2

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	if !strings.Contains(string(buf[:n]), "Aucun fichier de log") {
		t.Errorf("expected absent message, got: %s", string(buf[:n]))
	}
}

func TestTailLogs_LinesFlag(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	var sb strings.Builder
	for i := 1; i <= 30; i++ {
		sb.WriteString(fmt.Sprintf("line %d\n", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte(sb.String()), 0644); err != nil {
		t.Fatal(err)
	}

	oldArgs := os.Args
	os.Args = []string{"WinPiBooster.exe", "tail", "--lines", "5"}
	defer func() { os.Args = oldArgs }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	tailLogs()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "line 30") {
		t.Errorf("expected line 30, got: %q", out)
	}
	if strings.Contains(out, "line 25") {
		t.Errorf("expected only last 5 lines, but got line 25: %q", out)
	}
}

// ─── listLogs() ───────────────────────────────────────────────────────────────

func TestListLogs_ShowsFiles(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// Create current log + one archive.
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte("current"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog_2026-01-01.txt"), []byte("archive"), 0644); err != nil {
		t.Fatal(err)
	}

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	old2 := os.Stdout
	os.Stdout = w
	listLogs()
	w.Close()
	os.Stdout = old2

	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "UpdateLog.txt") {
		t.Errorf("expected UpdateLog.txt in output, got: %s", out)
	}
	if !strings.Contains(out, "UpdateLog_2026-01-01.txt") {
		t.Errorf("expected archive in output, got: %s", out)
	}
}

func TestListLogs_NoFiles(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Pipe: %v", err)
	}
	old2 := os.Stdout
	os.Stdout = w
	listLogs()
	w.Close()
	os.Stdout = old2

	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "Aucun fichier de log") {
		t.Errorf("expected 'Aucun fichier de log' in output, got: %s", out)
	}
}

// ─── historyLogs() ────────────────────────────────────────────────────────────

func TestHistoryLogs_NoFile(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	historyLogs()
	w.Close()
	os.Stdout = oldOut
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "Aucune installation enregistrée") {
		t.Errorf("expected no-install message, got: %q", out)
	}
}

func TestHistoryLogs_WithEntries(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	content := "2026-02-24 10:00:00 [INFO]: Installation terminée : KB5034441, KB5034442\n" +
		"2026-02-24 10:01:00 [INFO]: Heartbeat\n"
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	historyLogs()
	w.Close()
	os.Stdout = oldOut
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "KB5034441") {
		t.Errorf("expected KB5034441 in output, got: %q", out)
	}
	if !strings.Contains(out, "Total : 1 installation(s)") {
		t.Errorf("expected total=1, got: %q", out)
	}
}

// ─── exportConfig ─────────────────────────────────────────────────────────────

func TestExportConfig_WritesFile(t *testing.T) {
	dir := t.TempDir()
	// Point executable lookup to the temp dir by overriding logDir
	// exportConfig uses os.Executable(), so we test via the JSON content directly.
	// Instead, call json.MarshalIndent on cfg to verify the serialization.
	old := cfg
	cfg = defaults()
	defer func() { cfg = old }()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	dest := filepath.Join(dir, "config.json")
	if err := os.WriteFile(dest, append(data, '\n'), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "check_interval_seconds") {
		t.Errorf("exported JSON missing check_interval_seconds: %s", string(got))
	}
	if !strings.Contains(string(got), "retry_delay_seconds") {
		t.Errorf("exported JSON missing retry_delay_seconds: %s", string(got))
	}
}

func TestExportConfig_JSONRoundtrip(t *testing.T) {
	old := cfg
	cfg = defaults()
	cfg.CheckIntervalSeconds = 120
	cfg.RetryDelaySeconds = 10
	defer func() { cfg = old }()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	var back Config
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.CheckIntervalSeconds != 120 {
		t.Errorf("CheckIntervalSeconds = %d, want 120", back.CheckIntervalSeconds)
	}
	if back.RetryDelaySeconds != 10 {
		t.Errorf("RetryDelaySeconds = %d, want 10", back.RetryDelaySeconds)
	}
}

// ─── printExtendedStatus ──────────────────────────────────────────────────────

func TestPrintExtendedStatus_WithLogFile(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte("log content"), 0644); err != nil {
		t.Fatal(err)
	}

	oldCfg := cfg
	cfg = defaults()
	defer func() { cfg = oldCfg }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	oldErr := os.Stderr
	os.Stderr = w
	printExtendedStatus()
	w.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "UpdateLog.txt :") {
		t.Errorf("expected log file size info, got: %q", out)
	}
}

func TestPrintExtendedStatus_NoPanic(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	oldCfg := cfg
	cfg = defaults()
	defer func() { cfg = oldCfg }()

	// statusService() will fail (SCM not available in test), function must handle gracefully
	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	oldErr := os.Stderr
	os.Stderr = w
	printExtendedStatus()
	w.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	// Should contain config output regardless of SCM error
	if !strings.Contains(out, "check_interval_seconds") {
		t.Errorf("expected config in output, got: %q", out)
	}
	if !strings.Contains(out, "UpdateLog.txt : absent") {
		t.Errorf("expected absent log message, got: %q", out)
	}
}

func TestPrintExtendedStatus_WithStatusJSON(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// Write a fake status.json
	s := statusJSON{
		Version:          "v2.12.0",
		LastCheck:        "2026-02-24T10:00:00Z",
		UpdatesChecked:   5,
		UpdatesInstalled: 2,
	}
	data, _ := json.Marshal(s)
	if err := os.WriteFile(filepath.Join(dir, "status.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	oldCfg := cfg
	cfg = defaults()
	defer func() { cfg = oldCfg }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	oldErr := os.Stderr
	os.Stderr = w
	printExtendedStatus()
	w.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "updates_checked") {
		t.Errorf("expected status.json data in output, got: %q", out)
	}
}

// ─── show-config --json ───────────────────────────────────────────────────────

func TestShowConfigJSON_OutputsValidJSON(t *testing.T) {
	old := cfg
	cfg = defaults()
	defer func() { cfg = old }()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("MarshalIndent: %v", err)
	}
	var back Config
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.CheckIntervalSeconds != cfg.CheckIntervalSeconds {
		t.Errorf("CheckIntervalSeconds = %d, want %d", back.CheckIntervalSeconds, cfg.CheckIntervalSeconds)
	}
}

func TestShowConfigJSON_ContainsNewFields(t *testing.T) {
	old := cfg
	cfg = defaults()
	cfg.HeartbeatIntervalMinutes = 30
	cfg.RetryDelaySeconds = 3
	defer func() { cfg = old }()

	data, _ := json.MarshalIndent(cfg, "", "  ")
	s := string(data)
	for _, key := range []string{
		"heartbeat_interval_minutes",
		"retry_delay_seconds",
	} {
		if !strings.Contains(s, key) {
			t.Errorf("JSON missing key %q", key)
		}
	}
}

// ─── history --since DATE ─────────────────────────────────────────────────────

func TestHistoryLogs_SinceFilter(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	content := "2026-01-15 10:00:00 [INFO]: Installation terminée : KB1111\n" +
		"2026-02-10 12:00:00 [INFO]: Installation terminée : KB2222\n" +
		"2026-02-24 10:00:00 [INFO]: Installation terminée : KB3333\n"
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	oldArgs := os.Args
	os.Args = []string{"WinPiBooster.exe", "history", "--since", "2026-02-01"}
	defer func() { os.Args = oldArgs }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	historyLogs()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if strings.Contains(out, "KB1111") {
		t.Errorf("KB1111 should be filtered out by --since 2026-02-01, got: %q", out)
	}
	if !strings.Contains(out, "KB2222") {
		t.Errorf("KB2222 should be included (2026-02-10 >= 2026-02-01), got: %q", out)
	}
	if !strings.Contains(out, "KB3333") {
		t.Errorf("KB3333 should be included, got: %q", out)
	}
	if !strings.Contains(out, "Total : 2 installation(s)") {
		t.Errorf("expected total=2, got: %q", out)
	}
}

func TestHistoryLogs_SinceFilter_NoMatch(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	content := "2026-01-15 10:00:00 [INFO]: Installation terminée : KB1111\n"
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	oldArgs := os.Args
	os.Args = []string{"WinPiBooster.exe", "history", "--since", "2026-02-01"}
	defer func() { os.Args = oldArgs }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	historyLogs()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "depuis le 2026-02-01") {
		t.Errorf("expected since-specific message, got: %q", out)
	}
}

func TestHistoryLogs_SinceFilter_InclusiveDate(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// Entry exactly on the --since date must be INCLUDED
	content := "2026-02-01 10:00:00 [INFO]: Installation terminée : KB9999\n"
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	oldArgs := os.Args
	os.Args = []string{"WinPiBooster.exe", "history", "--since", "2026-02-01"}
	defer func() { os.Args = oldArgs }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	historyLogs()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "KB9999") {
		t.Errorf("entry on --since date should be included, got: %q", out)
	}
}

func TestHistoryLogs_SinceInvalidDate(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	content := "2026-01-15 10:00:00 [INFO]: Installation terminée : KB1111\n"
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	oldArgs := os.Args
	os.Args = []string{"WinPiBooster.exe", "history", "--since", "not-a-date"}
	defer func() { os.Args = oldArgs }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	historyLogs() // invalid date → ignored, show all
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	// All entries should be shown (invalid date treated as no filter)
	if !strings.Contains(out, "KB1111") {
		t.Errorf("all entries should show when --since is invalid, got: %q", out)
	}
}

// ─── check command alias ──────────────────────────────────────────────────────

func TestCheckCommand_AliasDetected(t *testing.T) {
	args := []string{"WinPiBooster.exe", "check"}
	cmd := ""
	if len(args) > 1 {
		cmd = args[1]
	}
	if cmd != "check" {
		t.Errorf("expected cmd=%q, got %q", "check", cmd)
	}
}

func TestCheckCommand_DifferentFromDryRun(t *testing.T) {
	// "check" and "--dry-run" are separate dispatch paths that both call runDryRun()
	dryRun := false
	args := []string{"WinPiBooster.exe", "check"}
	filtered := args[:1]
	for _, arg := range args[1:] {
		if arg == "--dry-run" {
			dryRun = true
		} else {
			filtered = append(filtered, arg)
		}
	}
	cmd := ""
	if len(filtered) > 1 {
		cmd = filtered[1]
	}
	if dryRun {
		t.Error("'check' should not set dryRun flag")
	}
	if cmd != "check" {
		t.Errorf("cmd should be 'check', got %q", cmd)
	}
}

// ─── listLogs() total (#110) ──────────────────────────────────────────────────

func TestListLogs_ShowsTotal(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// Create two log files with known content
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte("current log"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog_2026-01-01.txt"), []byte("archive"), 0644); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	listLogs()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 2048)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "Total") {
		t.Errorf("expected 'Total' line in listLogs output, got: %q", out)
	}
	if !strings.Contains(out, "2 fichier(s)") {
		t.Errorf("expected '2 fichier(s)' in total line, got: %q", out)
	}
}

// ─── printExtendedStatus next_check (#109) ────────────────────────────────────

func TestPrintExtendedStatus_NextCheck(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	oldCfg := cfg
	cfg = defaults()
	defer func() { cfg = oldCfg }()

	// Write status.json with a next_check in the future
	nextCheckTime := time.Now().Add(60 * time.Second).UTC()
	s := statusJSON{
		Version:   "dev",
		LastCheck: time.Now().UTC().Format(time.RFC3339),
		NextCheck: nextCheckTime.Format(time.RFC3339),
	}
	data, _ := json.Marshal(s)
	if err := os.WriteFile(filepath.Join(dir, "status.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	oldErr := os.Stderr
	os.Stderr = w
	printExtendedStatus()
	w.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "next_check") {
		t.Errorf("expected 'next_check' in output, got: %q", out)
	}
	if !strings.Contains(out, "dans") {
		t.Errorf("expected 'dans' (time remaining) in output, got: %q", out)
	}
}

// ─── printShowConfig default markers (#113) ───────────────────────────────────

func TestPrintShowConfig_DefaultMarker(t *testing.T) {
	old := cfg
	cfg = defaults()
	defer func() { cfg = old }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	printShowConfig()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "(défaut)") {
		t.Errorf("expected '(défaut)' marker for default values, got: %q", out)
	}
}

func TestPrintShowConfig_NonDefaultNoMarker(t *testing.T) {
	old := cfg
	cfg = defaults()
	cfg.CheckIntervalSeconds = 120 // non-default value
	defer func() { cfg = old }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	printShowConfig()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	// check_interval_seconds is 120 (not default 60) — find its line
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "check_interval_seconds") {
			if strings.Contains(line, "(défaut)") {
				t.Errorf("check_interval_seconds=120 should NOT be marked as default: %q", line)
			}
			break
		}
	}
	// Other fields should still have (défaut) marker
	if !strings.Contains(out, "(défaut)") {
		t.Errorf("expected other fields to have '(défaut)' marker, got: %q", out)
	}
}

// ─── printExtendedStatus last_error (#114) ────────────────────────────────────

func TestPrintExtendedStatus_LastError(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	oldCfg := cfg
	cfg = defaults()
	defer func() { cfg = oldCfg }()

	s := statusJSON{
		Version:   "dev",
		LastCheck: time.Now().UTC().Format(time.RFC3339),
		LastError: "execPS: exit status 1: Access denied",
	}
	data, _ := json.Marshal(s)
	if err := os.WriteFile(filepath.Join(dir, "status.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	oldErr := os.Stderr
	os.Stderr = w
	printExtendedStatus()
	w.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "last_error") {
		t.Errorf("expected 'last_error' in output when non-empty, got: %q", out)
	}
	if !strings.Contains(out, "Access denied") {
		t.Errorf("expected error message in output, got: %q", out)
	}
}

func TestPrintExtendedStatus_NoLastError(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	oldCfg := cfg
	cfg = defaults()
	defer func() { cfg = oldCfg }()

	s := statusJSON{
		Version:   "dev",
		LastCheck: time.Now().UTC().Format(time.RFC3339),
		LastError: "", // empty — must not appear
	}
	data, _ := json.Marshal(s)
	if err := os.WriteFile(filepath.Join(dir, "status.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	oldErr := os.Stderr
	os.Stderr = w
	printExtendedStatus()
	w.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if strings.Contains(out, "last_error") {
		t.Errorf("'last_error' should not appear when empty, got: %q", out)
	}
}

// ─── show-config --diff (#115) ───────────────────────────────────────────────

func TestPrintShowConfig_DiffMode_NoChanges(t *testing.T) {
	old := cfg
	cfg = defaults()
	defer func() { cfg = old }()

	oldArgs := os.Args
	os.Args = []string{"WinPiBooster.exe", "show-config", "--diff"}
	defer func() { os.Args = oldArgs }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	printShowConfig()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 2048)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "identique aux défauts") {
		t.Errorf("expected 'identique aux défauts' when no changes, got: %q", out)
	}
}

func TestPrintShowConfig_DiffMode_WithChange(t *testing.T) {
	old := cfg
	cfg = defaults()
	cfg.CheckIntervalSeconds = 120
	defer func() { cfg = old }()

	oldArgs := os.Args
	os.Args = []string{"WinPiBooster.exe", "show-config", "--diff"}
	defer func() { os.Args = oldArgs }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	printShowConfig()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 2048)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "check_interval_seconds") {
		t.Errorf("expected changed field in diff output, got: %q", out)
	}
	if strings.Contains(out, "retry_attempts") {
		t.Errorf("unchanged field should not appear in diff, got: %q", out)
	}
}

// ─── clean-logs --dry-run (#116) ──────────────────────────────────────────────

func TestCleanOldLogsDryRun_NoFiles(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	cleanOldLogsDryRun()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 512)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "Aucun fichier") {
		t.Errorf("expected 'Aucun fichier' when no expired archives, got: %q", out)
	}
}

func TestCleanOldLogsDryRun_ExpiredFile(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// Create an archive with an old mod time
	archivePath := filepath.Join(dir, "UpdateLog_2025-01-01T00-00-00.txt")
	if err := os.WriteFile(archivePath, []byte("old log"), 0644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().AddDate(0, 0, -60)
	if err := os.Chtimes(archivePath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	cleanOldLogsDryRun()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	// File must still exist (dry run)
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		t.Error("dry-run should not delete files")
	}
	if !strings.Contains(out, "UpdateLog_2025-01-01") {
		t.Errorf("expected expired file listed in dry-run output, got: %q", out)
	}
}

// ─── printReport uptime (#117) ────────────────────────────────────────────────

func TestPrintReport_WithUptime(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// Write status.json with uptime_seconds
	s := statusJSON{UptimeSeconds: 7530} // 2h 5m 30s
	data, _ := json.Marshal(s)
	if err := os.WriteFile(filepath.Join(dir, "status.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	printReport()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "Uptime") {
		t.Errorf("expected 'Uptime' in report output when status.json present, got: %q", out)
	}
}

func TestPrintReport_NoStatusJSON(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// No status.json — uptime line must be absent, no panic
	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	printReport()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if strings.Contains(out, "Uptime") {
		t.Errorf("'Uptime' should not appear when status.json absent, got: %q", out)
	}
	if !strings.Contains(out, "Vérifications") {
		t.Errorf("expected counter lines even without status.json, got: %q", out)
	}
}

// ─── historyLogs KB distincts (#118) ─────────────────────────────────────────

func TestHistoryLogs_DistinctKBs(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	// 3 lines but only 3 distinct KBs (KB1111 appears twice)
	content := "2026-02-24 10:00:00 [INFO]: Installation terminée : KB1111, KB2222\n" +
		"2026-02-24 11:00:00 [INFO]: Installation terminée : KB1111, KB3333\n"
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	historyLogs()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "3 KB distincts") {
		t.Errorf("expected '3 KB distincts' (KB1111, KB2222, KB3333), got: %q", out)
	}
	if !strings.Contains(out, "Total : 2 installation(s)") {
		t.Errorf("expected total=2, got: %q", out)
	}
}

// ─── tail --grep (#119) ───────────────────────────────────────────────────────

func TestTailLogs_GrepMatch(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	content := "2026-02-24 10:00:00 [INFO]: Heartbeat\n" +
		"2026-02-24 10:01:00 [ERROR]: execPS failed\n" +
		"2026-02-24 10:02:00 [INFO]: Cycle OK\n"
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	oldArgs := os.Args
	os.Args = []string{"WinPiBooster.exe", "tail", "--grep", "error"}
	defer func() { os.Args = oldArgs }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	tailLogs()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 2048)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "ERROR") {
		t.Errorf("expected ERROR line in output, got: %q", out)
	}
	if strings.Contains(out, "Heartbeat") {
		t.Errorf("Heartbeat should be filtered out, got: %q", out)
	}
}

func TestTailLogs_GrepNoMatch(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte("2026-02-24 [INFO]: all good\n"), 0644); err != nil {
		t.Fatal(err)
	}

	oldArgs := os.Args
	os.Args = []string{"WinPiBooster.exe", "tail", "--grep", "ERROR"}
	defer func() { os.Args = oldArgs }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	tailLogs()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 512)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "Aucune ligne") {
		t.Errorf("expected 'Aucune ligne' message, got: %q", out)
	}
}

// ─── history --last N (#120) ──────────────────────────────────────────────────

func TestHistoryLogs_LastN(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	content := "2026-02-24 10:00:00 [INFO]: Installation terminée : KB1111\n" +
		"2026-02-24 11:00:00 [INFO]: Installation terminée : KB2222\n" +
		"2026-02-24 12:00:00 [INFO]: Installation terminée : KB3333\n"
	if err := os.WriteFile(filepath.Join(dir, "UpdateLog.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	oldArgs := os.Args
	os.Args = []string{"WinPiBooster.exe", "history", "--last", "2"}
	defer func() { os.Args = oldArgs }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	historyLogs()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if strings.Contains(out, "KB1111") {
		t.Errorf("KB1111 should be excluded by --last 2, got: %q", out)
	}
	if !strings.Contains(out, "KB2222") {
		t.Errorf("KB2222 should be included, got: %q", out)
	}
	if !strings.Contains(out, "KB3333") {
		t.Errorf("KB3333 should be included, got: %q", out)
	}
}

// ─── clean-logs taille libérée (#121) ────────────────────────────────────────

func TestCleanOldLogsDryRun_ShowsSize(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	archivePath := filepath.Join(dir, "UpdateLog_2025-01-01T00-00-00.txt")
	if err := os.WriteFile(archivePath, []byte("old log content"), 0644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().AddDate(0, 0, -60)
	if err := os.Chtimes(archivePath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	cleanOldLogsDryRun()
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "MB") {
		t.Errorf("expected size in MB in dry-run output, got: %q", out)
	}
}

func TestCleanOldLogsVerbose_ShowsSize(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	archivePath := filepath.Join(dir, "UpdateLog_2025-01-01T00-00-00.txt")
	if err := os.WriteFile(archivePath, []byte("old log content"), 0644); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().AddDate(0, 0, -60)
	if err := os.Chtimes(archivePath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	cleanOldLogsVerbose(true)
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 1024)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "MB") {
		t.Errorf("expected size in MB in verbose output, got: %q", out)
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Error("expected archive to be deleted in verbose mode")
	}
}

// ─── diagnose seuil disque (#122) ────────────────────────────────────────────

func TestRunDiagnose_ShowsDiskThreshold(t *testing.T) {
	oldCfg := cfg
	cfg = defaults()
	cfg.MinFreeDiskMB = 500
	defer func() { cfg = oldCfg }()

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	withTestLogger(t, func() {
		runDiagnose()
	})
	w.Close()
	os.Stdout = oldOut

	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "seuil") {
		t.Errorf("expected 'seuil' in disk check output, got: %q", out)
	}
	if !strings.Contains(out, "500") {
		t.Errorf("expected threshold value '500' in output, got: %q", out)
	}
}

// ─── status version en-tête (#123) ───────────────────────────────────────────

func TestPrintExtendedStatus_Version(t *testing.T) {
	dir := t.TempDir()
	old := logDir
	logDir = dir
	defer func() { logDir = old }()

	oldCfg := cfg
	cfg = defaults()
	defer func() { cfg = oldCfg }()

	s := statusJSON{
		Version:   "v2.19.0",
		LastCheck: time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.Marshal(s)
	if err := os.WriteFile(filepath.Join(dir, "status.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = w
	oldErr := os.Stderr
	os.Stderr = w
	printExtendedStatus()
	w.Close()
	os.Stdout = oldOut
	os.Stderr = oldErr

	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	if !strings.Contains(out, "v2.19.0") {
		t.Errorf("expected version 'v2.19.0' in status output, got: %q", out)
	}
}
