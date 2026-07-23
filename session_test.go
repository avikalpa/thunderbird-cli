package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// environBlob builds a /proc/<pid>/environ-shaped payload.
func environBlob(entries ...string) string {
	return strings.Join(entries, "\x00") + "\x00"
}

func TestParseSessionEnvironDropsSandboxOnlyPaths(t *testing.T) {
	t.Parallel()

	runtimeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runtimeDir, "wayland-0"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	xauth := filepath.Join(runtimeDir, "xauth_test")
	if err := os.WriteFile(xauth, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	// A Flatpak Betterbird reports the bus path it sees *inside* the sandbox.
	// Adopting it verbatim would break the launch we are trying to fix.
	session := parseSessionEnviron(environBlob(
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR="+runtimeDir,
		"XAUTHORITY="+xauth,
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/flatpak/bus",
		"IRRELEVANT=1",
	))

	if !session.usable() {
		t.Fatal("session with a validated Wayland display reported unusable")
	}
	if got := session.Vars["DBUS_SESSION_BUS_ADDRESS"]; strings.Contains(got, "/run/flatpak/bus") {
		t.Fatalf("adopted sandbox-only bus path: %q", got)
	}
	if session.Vars["XAUTHORITY"] != xauth {
		t.Fatalf("XAUTHORITY = %q, want %q", session.Vars["XAUTHORITY"], xauth)
	}
	if _, ok := session.Vars["IRRELEVANT"]; ok {
		t.Fatal("harvested a variable outside sessionVars")
	}
}

func TestParseSessionEnvironAdoptsHostBusWhenPresent(t *testing.T) {
	t.Parallel()

	runtimeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runtimeDir, "wayland-0"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	busPath := filepath.Join(runtimeDir, "bus")
	if err := os.WriteFile(busPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	session := parseSessionEnviron(environBlob(
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR="+runtimeDir,
		"DBUS_SESSION_BUS_ADDRESS=unix:path=/run/flatpak/bus",
	))
	if got := session.Vars["DBUS_SESSION_BUS_ADDRESS"]; got != "unix:path="+busPath {
		t.Fatalf("DBUS_SESSION_BUS_ADDRESS = %q, want the host bus %q", got, "unix:path="+busPath)
	}
}

func TestParseSessionEnvironRejectsDanglingDisplay(t *testing.T) {
	t.Parallel()

	// WAYLAND_DISPLAY naming a socket that does not exist on this host must not
	// count as a usable session, or tb would "join" a session that is not there.
	session := parseSessionEnviron(environBlob(
		"WAYLAND_DISPLAY=wayland-0",
		"XDG_RUNTIME_DIR=/nonexistent-runtime-dir",
	))
	if session.usable() {
		t.Fatalf("dangling Wayland session reported usable: %+v", session.Vars)
	}
}

func TestParseSessionEnvironKeepsX11Display(t *testing.T) {
	t.Parallel()

	session := parseSessionEnviron(environBlob("DISPLAY=:1"))
	if !session.usable() {
		t.Fatal("X11 DISPLAY not reported usable")
	}
	if session.display() != "DISPLAY=:1" {
		t.Fatalf("display() = %q", session.display())
	}
}

func TestSessionEnvApplyOverridesExisting(t *testing.T) {
	t.Parallel()

	session := sessionEnv{Vars: map[string]string{"DISPLAY": ":1"}}
	got := session.apply([]string{"DISPLAY=:99", "PATH=/usr/bin"})

	var displays []string
	for _, entry := range got {
		if strings.HasPrefix(entry, "DISPLAY=") {
			displays = append(displays, entry)
		}
	}
	if len(displays) != 1 || displays[0] != "DISPLAY=:1" {
		t.Fatalf("DISPLAY entries = %v, want exactly [DISPLAY=:1]", displays)
	}
	if !containsString(got, "PATH=/usr/bin") {
		t.Fatal("apply() dropped an unrelated variable")
	}
}

func TestDBusUnixPath(t *testing.T) {
	t.Parallel()

	if path, ok := dbusUnixPath("unix:path=/run/user/1000/bus"); !ok || path != "/run/user/1000/bus" {
		t.Fatalf("dbusUnixPath() = %q, %v", path, ok)
	}
	if path, ok := dbusUnixPath("unix:abstract=/tmp/dbus-abc"); ok {
		t.Fatalf("abstract socket treated as a filesystem path: %q", path)
	}
}

func containsString(haystack []string, want string) bool {
	for _, entry := range haystack {
		if entry == want {
			return true
		}
	}
	return false
}
