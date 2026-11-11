package main

import (
	"strings"
	"testing"

	"lionsql/pkg/config"
	"lionsql/pkg/storage/memengine"
)

func TestStartupBanner_DefaultPassword_LastLineMustBeWarning(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Auth.AdminPass = config.DefaultAdminPass

	ctx := bannerContext{
		HasHTTP: true,
		HasPG:   true,
		HasTDS:  true,
	}

	banner := renderStartupBanner(cfg, ctx)
	lines := strings.Split(strings.TrimSpace(banner), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected non-empty banner")
	}

	lastLine := strings.TrimSpace(lines[len(lines)-1])
	if lastLine != config.DefaultAdminPassWarning {
		t.Fatalf("expected last line of startup banner to be exact warning %q, but got %q\nFull banner:\n%s",
			config.DefaultAdminPassWarning, lastLine, banner)
	}

	// Verify the warning contains no password literals
	if strings.Contains(lastLine, "1234567890") {
		t.Errorf("warning must NOT leak password literal '1234567890', but found it in: %q", lastLine)
	}

	// Verify the warning appears exactly once in the banner
	if count := strings.Count(banner, config.DefaultAdminPassWarning); count != 1 {
		t.Fatalf("expected warning to appear exactly once in banner, got %d times", count)
	}
}

func TestStartupBanner_EmptyPassword_LastLineMustBeWarning(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Auth.AdminPass = "" // empty string counts as default insecure

	ctx := bannerContext{
		HasHTTP: true,
	}

	banner := renderStartupBanner(cfg, ctx)
	lines := strings.Split(strings.TrimSpace(banner), "\n")
	if len(lines) == 0 {
		t.Fatalf("expected non-empty banner")
	}

	lastLine := strings.TrimSpace(lines[len(lines)-1])
	if lastLine != config.DefaultAdminPassWarning {
		t.Fatalf("expected last line to be exact warning %q, but got %q", config.DefaultAdminPassWarning, lastLine)
	}
}

func TestStartupBanner_CustomPassword_NoWarning(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Auth.AdminPass = "SuperSecretCustomPassword987!"

	ctx := bannerContext{
		HasHTTP: true,
	}

	banner := renderStartupBanner(cfg, ctx)
	if strings.Contains(banner, config.DefaultAdminPassWarning) {
		t.Fatalf("banner must NOT contain default password warning when custom password is set")
	}

	lines := strings.Split(strings.TrimSpace(banner), "\n")
	lastLine := strings.TrimSpace(lines[len(lines)-1])
	if lastLine != "Press Ctrl+C to stop server" {
		t.Fatalf("expected last line with custom password to be 'Press Ctrl+C to stop server', got %q", lastLine)
	}
}

func TestDaemonStartupOutput_DefaultPassword_LastLineMustBeWarning(t *testing.T) {
	out := renderDaemonStartupOutput(12345, "mem", "data", "data/server.log", "data/server.pid", "127.0.0.1:8090", "127.0.0.1:5433", "127.0.0.1:1433", config.DefaultAdminPass)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	lastLine := strings.TrimSpace(lines[len(lines)-1])

	if lastLine != config.DefaultAdminPassWarning {
		t.Fatalf("expected last line of daemon output to be exact warning %q, got %q\nFull output:\n%s",
			config.DefaultAdminPassWarning, lastLine, out)
	}
}

func TestDaemonStartupOutput_CustomPassword_NoWarning(t *testing.T) {
	out := renderDaemonStartupOutput(12345, "mem", "data", "data/server.log", "data/server.pid", "127.0.0.1:8090", "127.0.0.1:5433", "127.0.0.1:1433", "MyCustomPassword!")
	if strings.Contains(out, config.DefaultAdminPassWarning) {
		t.Fatalf("daemon output must NOT contain warning when custom password is set")
	}
}

func TestStartupBanner_MemoryGovernorAlignment(t *testing.T) {
	cfg := config.DefaultConfig()
	ctx := bannerContext{
		HasHTTP:        true,
		WorkMem:        "64MB",
		GlobalMemLimit: 4096 * 1024 * 1024,
	}

	banner := renderStartupBanner(cfg, ctx)
	expectedMaxMem := " [Memory Governor] max_memory: 4096MB (Go runtime GC soft limit)"
	expectedWorkMem := "                   work_mem:   64MB (External Merge Sort & HashJoin guard)"

	if !strings.Contains(banner, expectedMaxMem) {
		t.Fatalf("expected banner to contain %q, but got:\n%s", expectedMaxMem, banner)
	}
	if !strings.Contains(banner, expectedWorkMem) {
		t.Fatalf("expected banner to contain %q, but got:\n%s", expectedWorkMem, banner)
	}
}

func TestStartupBanner_MemoryPerformanceDeprecationNotice(t *testing.T) {
	ctx := bannerContext{
		HasHTTP: true,
	}

	// Default compact mode must remain clean without deprecation notice.
	defaultCfg := config.DefaultConfig()
	defaultCfg.Auth.AdminPass = "strong-non-default-pass"
	defaultBanner := renderStartupBanner(defaultCfg, ctx)
	if !strings.Contains(defaultBanner, "Memory: compact\n") {
		t.Fatalf("expected default banner to show clean Memory: compact, got:\n%s", defaultBanner)
	}
	if strings.Contains(defaultBanner, "DEPRECATED") {
		t.Fatalf("did not expect DEPRECATED tag for default compact mode, got:\n%s", defaultBanner)
	}

	// Performance mode stays operational in v1.0 while emitting explicit deprecation label in banner.
	perfCfg := config.DefaultConfig()
	perfCfg.Auth.AdminPass = "strong-non-default-pass"
	perfCfg.Server.Memory = "performance"
	if got := perfCfg.Server.GetMemory(); got != "performance" {
		t.Fatalf("expected GetMemory() to remain %q in v1.0, got %q", "performance", got)
	}
	perfBanner := renderStartupBanner(perfCfg, ctx)
	if !strings.Contains(perfBanner, "Memory: performance [DEPRECATED: use compact]\n") {
		t.Fatalf("expected performance banner to include deprecation notice, got:\n%s", perfBanner)
	}

	// Last-line invariant must stay intact even with deprecation notice.
	lines := strings.Split(strings.TrimSpace(perfBanner), "\n")
	if lastLine := strings.TrimSpace(lines[len(lines)-1]); lastLine != "Press Ctrl+C to stop server" {
		t.Fatalf("expected last line invariant preserved, got %q", lastLine)
	}

	// Verify helper notice output for both modes.
	if notice := memoryPerformanceDeprecationNotice(memengine.MemoryPerformance); !strings.Contains(notice, "DEPRECATED") || !strings.Contains(notice, "compact") {
		t.Fatalf("expected deprecation notice for MemoryPerformance, got %q", notice)
	}
	if notice := memoryPerformanceDeprecationNotice(memengine.MemoryCompact); notice != "" {
		t.Fatalf("expected empty notice for MemoryCompact, got %q", notice)
	}
}
