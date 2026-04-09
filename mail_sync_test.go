package main

import (
	"os"
	"testing"
	"time"
)

func TestMailCommandUsesFlatpak(t *testing.T) {
	t.Parallel()
	if !mailCommandUsesFlatpak([]string{"/usr/bin/flatpak", "run", "eu.betterbird.Betterbird"}) {
		t.Fatal("expected flatpak command to be detected")
	}
	if mailCommandUsesFlatpak([]string{"/usr/bin/betterbird"}) {
		t.Fatal("did not expect native binary to be treated as flatpak")
	}
}

func TestValidateSyncEnvironmentRejectsHeadlessFlatpak(t *testing.T) {
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("MIR_SOCKET", "")
	if err := validateSyncEnvironment([]string{"/usr/bin/flatpak", "run", "eu.betterbird.Betterbird"}); err == nil {
		t.Fatal("expected headless flatpak sync to be rejected")
	}
}

func TestValidateSyncEnvironmentAllowsNativeBinary(t *testing.T) {
	t.Setenv("DISPLAY", "")
	t.Setenv("WAYLAND_DISPLAY", "")
	t.Setenv("MIR_SOCKET", "")
	if err := validateSyncEnvironment([]string{"/usr/bin/betterbird"}); err != nil {
		t.Fatalf("expected native binary sync to pass: %v", err)
	}
}

func TestSyncTimeoutDefaultAndOverride(t *testing.T) {
	os.Unsetenv("TB_SYNC_TIMEOUT")
	if got := syncTimeout(); got != 90*time.Second {
		t.Fatalf("default timeout = %s, want 90s", got)
	}
	t.Setenv("TB_SYNC_TIMEOUT", "15s")
	if got := syncTimeout(); got != 15*time.Second {
		t.Fatalf("override timeout = %s, want 15s", got)
	}
	t.Setenv("TB_SYNC_TIMEOUT", "bogus")
	if got := syncTimeout(); got != 90*time.Second {
		t.Fatalf("invalid timeout should fall back to 90s, got %s", got)
	}
}
