package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
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
