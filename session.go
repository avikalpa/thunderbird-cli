package main

// GUI session discovery.
//
// tb drives Thunderbird/Betterbird, and both need a display plus a session bus.
// A plain ssh shell has neither in its environment, and the old behaviour was to
// warn ("no DISPLAY environment variable specified") and fall through to a
// stale-cache read — a --sync that silently did nothing while looking like it
// worked. When the mail client is already running for this user, its own
// process environment is the authoritative session to join, so tb harvests it
// from /proc instead of guessing.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sessionVars are the variables that decide whether a GUI mail client can start.
var sessionVars = []string{
	"DISPLAY",
	"WAYLAND_DISPLAY",
	"XAUTHORITY",
	"XDG_RUNTIME_DIR",
	"DBUS_SESSION_BUS_ADDRESS",
}

type sessionEnv struct {
	Vars   map[string]string
	Source string
}

// display renders the session for a log line.
func (s sessionEnv) display() string {
	if d := s.Vars["WAYLAND_DISPLAY"]; d != "" {
		return "WAYLAND_DISPLAY=" + d
	}
	if d := s.Vars["DISPLAY"]; d != "" {
		return "DISPLAY=" + d
	}
	return "(no display)"
}

// usable reports whether the harvested session actually names a display.
func (s sessionEnv) usable() bool {
	return s.Vars["DISPLAY"] != "" || s.Vars["WAYLAND_DISPLAY"] != ""
}

// apply overlays the session onto an environment slice, replacing any existing
// definitions of the same variables.
func (s sessionEnv) apply(env []string) []string {
	out := make([]string, 0, len(env)+len(s.Vars))
	for _, entry := range env {
		key, _, ok := strings.Cut(entry, "=")
		if ok {
			if _, override := s.Vars[key]; override {
				continue
			}
		}
		out = append(out, entry)
	}
	for key, value := range s.Vars {
		out = append(out, key+"="+value)
	}
	return out
}

// detectRunningMailSession finds a running Thunderbird/Betterbird belonging to
// this user and adopts its GUI session environment.
//
// Values are validated against the host filesystem before being adopted: a
// Flatpak mail client reports sandbox-internal paths such as
// DBUS_SESSION_BUS_ADDRESS=unix:path=/run/flatpak/bus, which do not exist
// outside the sandbox and would break the launch we are trying to fix.
func detectRunningMailSession() (sessionEnv, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return sessionEnv{}, false // not Linux, or /proc unavailable
	}
	self := os.Getpid()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == self {
			continue
		}
		if !processIsMailClient(pid) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if err != nil || len(raw) == 0 {
			// Flatpak's outer bwrap wrapper has an empty environ; the inner
			// process carries the real one, so keep scanning.
			continue
		}
		session := parseSessionEnviron(string(raw))
		if session.usable() {
			session.Source = fmt.Sprintf("running mail client (pid %d)", pid)
			return session, true
		}
	}
	return sessionEnv{}, false
}

// processIsMailClient reports whether a pid looks like Thunderbird/Betterbird.
func processIsMailClient(pid int) bool {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return false
	}
	cmdline := strings.ToLower(strings.ReplaceAll(string(raw), "\x00", " "))
	if !strings.Contains(cmdline, "thunderbird") && !strings.Contains(cmdline, "betterbird") {
		return false
	}
	// Content/renderer children carry the same session but are noisier to
	// attribute; the parent process is enough and is scanned first in pid order.
	return true
}

// parseSessionEnviron extracts and validates the session variables from a raw
// /proc/<pid>/environ blob.
func parseSessionEnviron(blob string) sessionEnv {
	want := map[string]bool{}
	for _, key := range sessionVars {
		want[key] = true
	}
	found := map[string]string{}
	for _, entry := range strings.Split(blob, "\x00") {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || !want[key] || value == "" {
			continue
		}
		found[key] = value
	}

	vars := map[string]string{}
	for key, value := range found {
		if sessionValueUsable(key, value, found) {
			vars[key] = value
		}
	}
	// A Wayland display name is relative to XDG_RUNTIME_DIR; without a usable
	// runtime dir it means nothing.
	if vars["XDG_RUNTIME_DIR"] == "" {
		delete(vars, "WAYLAND_DISPLAY")
	}
	// Prefer the host's own session bus when the client reported a sandboxed one.
	if vars["DBUS_SESSION_BUS_ADDRESS"] == "" && vars["XDG_RUNTIME_DIR"] != "" {
		hostBus := filepath.Join(vars["XDG_RUNTIME_DIR"], "bus")
		if pathExists(hostBus) {
			vars["DBUS_SESSION_BUS_ADDRESS"] = "unix:path=" + hostBus
		}
	}
	return sessionEnv{Vars: vars}
}

// sessionValueUsable rejects values that point at paths absent on this host,
// which is how sandbox-internal settings are filtered out.
func sessionValueUsable(key, value string, found map[string]string) bool {
	switch key {
	case "XAUTHORITY", "XDG_RUNTIME_DIR":
		return pathExists(value)
	case "DBUS_SESSION_BUS_ADDRESS":
		path, ok := dbusUnixPath(value)
		if !ok {
			return true // abstract sockets and other transports: leave as-is
		}
		return pathExists(path)
	case "WAYLAND_DISPLAY":
		if filepath.IsAbs(value) {
			return pathExists(value)
		}
		runtimeDir := found["XDG_RUNTIME_DIR"]
		if runtimeDir == "" {
			return false
		}
		return pathExists(filepath.Join(runtimeDir, value))
	}
	return true
}

// dbusUnixPath pulls the filesystem path out of a DBus address, if it has one.
func dbusUnixPath(address string) (string, bool) {
	for _, field := range strings.Split(address, ",") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(field), "unix:path="); ok {
			return rest, true
		}
	}
	return "", false
}

func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}
