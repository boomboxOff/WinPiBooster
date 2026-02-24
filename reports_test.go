package main

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// ─── durationUntilMidnight() ──────────────────────────────────────────────────

func TestDurationUntilMidnight_Positive(t *testing.T) {
	d := durationUntilMidnight()
	if d <= 0 {
		t.Errorf("durationUntilMidnight() = %v, want > 0", d)
	}
	if d > 24*time.Hour {
		t.Errorf("durationUntilMidnight() = %v, want <= 24h", d)
	}
}

// ─── buildDailyReport / cycleErrors ───────────────────────────────────────────

func TestBuildDailyReport_IncludesAllFields(t *testing.T) {
	report := buildDailyReport(10, 3, 5, 2)
	for _, want := range []string{
		"Vérifications totales : 10",
		"Mises à jour installées : 3",
		"Vérifications sans mise à jour : 5",
		"Erreurs : 2",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q\nfull report: %s", want, report)
		}
	}
}

func TestBuildDailyReport_ZeroErrors(t *testing.T) {
	report := buildDailyReport(5, 1, 4, 0)
	if !strings.Contains(report, "Erreurs : 0") {
		t.Errorf("report should contain 'Erreurs : 0'\nfull report: %s", report)
	}
}

func TestCycleErrors_Reset(t *testing.T) {
	atomic.StoreInt64(&cycleErrors, 7)
	got := atomic.SwapInt64(&cycleErrors, 0)
	if got != 7 {
		t.Errorf("cycleErrors = %d, want 7", got)
	}
	if after := atomic.LoadInt64(&cycleErrors); after != 0 {
		t.Errorf("cycleErrors after reset = %d, want 0", after)
	}
}

// ─── formatUptime() ───────────────────────────────────────────────────────────

func TestFormatUptime(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "0m 30s"},
		{90 * time.Second, "1m 30s"},
		{1*time.Hour + 5*time.Minute + 3*time.Second, "1h 5m 3s"},
		{2*time.Hour + 0*time.Minute + 0*time.Second, "2h 0m 0s"},
		{0, "0m 0s"},
	}
	for _, c := range cases {
		if got := formatUptime(c.d); got != c.want {
			t.Errorf("formatUptime(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

// ─── weekly report ────────────────────────────────────────────────────────────

func TestBuildWeeklyReport(t *testing.T) {
	report := buildWeeklyReport(70, 21, 42, 7)
	for _, want := range []string{"hebdomadaire", "70", "21", "42", "7"} {
		if !strings.Contains(report, want) {
			t.Errorf("buildWeeklyReport missing %q, got: %s", want, report)
		}
	}
}

func TestDurationUntilNextSunday_Positive(t *testing.T) {
	d := durationUntilNextSunday()
	if d <= 0 || d > 7*24*time.Hour {
		t.Errorf("durationUntilNextSunday() = %v, want (0, 7d]", d)
	}
}

func TestWeeklyCounters_AccumulatedByDaily(t *testing.T) {
	atomic.StoreInt64(&updatesChecked, 5)
	atomic.StoreInt64(&updatesInstalled, 2)
	atomic.StoreInt64(&updatesSkipped, 3)
	atomic.StoreInt64(&cycleErrors, 1)
	atomic.StoreInt64(&weeklyChecked, 0)
	atomic.StoreInt64(&weeklyInstalled, 0)
	atomic.StoreInt64(&weeklySkipped, 0)
	atomic.StoreInt64(&weeklyErrors, 0)
	defer func() {
		for _, p := range []*int64{&updatesChecked, &updatesInstalled, &updatesSkipped, &cycleErrors,
			&weeklyChecked, &weeklyInstalled, &weeklySkipped, &weeklyErrors} {
			atomic.StoreInt64(p, 0)
		}
	}()

	generateDailyReport()

	if atomic.LoadInt64(&weeklyChecked) != 5 {
		t.Errorf("weeklyChecked = %d, want 5", atomic.LoadInt64(&weeklyChecked))
	}
	if atomic.LoadInt64(&weeklyInstalled) != 2 {
		t.Errorf("weeklyInstalled = %d, want 2", atomic.LoadInt64(&weeklyInstalled))
	}
}

// ─── generateWeeklyReport ─────────────────────────────────────────────────────

func TestGenerateWeeklyReport_NilLog(t *testing.T) {
	atomic.StoreInt64(&weeklyChecked, 3)
	atomic.StoreInt64(&weeklyInstalled, 1)
	atomic.StoreInt64(&weeklySkipped, 2)
	atomic.StoreInt64(&weeklyErrors, 0)
	defer func() {
		for _, p := range []*int64{&weeklyChecked, &weeklyInstalled, &weeklySkipped, &weeklyErrors} {
			atomic.StoreInt64(p, 0)
		}
	}()

	generateWeeklyReport() // log is nil, must not panic

	// counters should be reset to 0
	if atomic.LoadInt64(&weeklyChecked) != 0 {
		t.Errorf("weeklyChecked should be 0 after report, got %d", atomic.LoadInt64(&weeklyChecked))
	}
}

// ─── buildWeeklyReport edge cases ─────────────────────────────────────────────

func TestBuildWeeklyReport_Zeros(t *testing.T) {
	got := buildWeeklyReport(0, 0, 0, 0)
	if !strings.Contains(got, "Rapport hebdomadaire") {
		t.Errorf("expected weekly report header, got: %q", got)
	}
}

func TestBuildWeeklyReport_LargeValues(t *testing.T) {
	got := buildWeeklyReport(1000, 500, 400, 100)
	if !strings.Contains(got, "1000") || !strings.Contains(got, "500") {
		t.Errorf("expected large values in report, got: %q", got)
	}
}

// ─── heartbeat() with logger ──────────────────────────────────────────────────

func TestHeartbeat_WithLogger(t *testing.T) {
	withTestLogger(t, func() {
		old := startTime
		startTime = time.Now().Add(-2 * time.Minute)
		defer func() { startTime = old }()
		heartbeat() // must not panic
	})
}

// ─── generateDailyReport / generateWeeklyReport with logger ──────────────────

func TestGenerateDailyReport_WithLogger(t *testing.T) {
	withTestLogger(t, func() {
		atomic.StoreInt64(&updatesChecked, 4)
		atomic.StoreInt64(&updatesInstalled, 1)
		atomic.StoreInt64(&updatesSkipped, 3)
		atomic.StoreInt64(&cycleErrors, 0)
		defer func() {
			for _, p := range []*int64{&weeklyChecked, &weeklyInstalled, &weeklySkipped, &weeklyErrors} {
				atomic.StoreInt64(p, 0)
			}
		}()
		generateDailyReport()
		if got := atomic.LoadInt64(&weeklyChecked); got != 4 {
			t.Errorf("weeklyChecked = %d, want 4", got)
		}
	})
}

func TestGenerateWeeklyReport_WithLogger(t *testing.T) {
	withTestLogger(t, func() {
		atomic.StoreInt64(&weeklyChecked, 7)
		atomic.StoreInt64(&weeklyInstalled, 3)
		atomic.StoreInt64(&weeklySkipped, 4)
		atomic.StoreInt64(&weeklyErrors, 0)
		defer func() {
			for _, p := range []*int64{&weeklyChecked, &weeklyInstalled, &weeklySkipped, &weeklyErrors} {
				atomic.StoreInt64(p, 0)
			}
		}()
		generateWeeklyReport()
		if got := atomic.LoadInt64(&weeklyChecked); got != 0 {
			t.Errorf("weeklyChecked should be reset to 0, got %d", got)
		}
	})
}
