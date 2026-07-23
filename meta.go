package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

var (
	version = "3.4.0"
	commit  = "unknown"
	builtAt = "unknown"
)

const (
	releaseRepoOwner = "avikalpa"
	releaseRepoName  = "thunderbird-cli"
)

type releaseInfo struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func printVersion() {
	fmt.Printf("thunderbird-cli %s\n", version)
	fmt.Printf("commit: %s\n", commit)
	fmt.Printf("go:     %s/%s\n", runtime.GOOS, runtime.GOARCH)
	fmt.Printf("direct: %s\n", nssBuildDetail())
	if builtAt != "" && builtAt != "unknown" {
		fmt.Printf("built:  %s\n", builtAt)
	}
}

func printFeatures() {
	store := selectedStoreBackend()
	fmt.Printf("Version: %s\n", version)
	fmt.Printf("Default backend: %s\n", storeBackendSQLite)
	fmt.Printf("Selected backend: %s\n", store)
	fmt.Printf("SQLite path: %s\n", defaultSQLitePath())
	fmt.Printf("Direct secret-backed send build: %s\n", nssBuildDetail())
	fmt.Println("Direct headless send providers:")
	if nssDirectSendCompiled() {
		fmt.Println("- Google / Gmail")
		fmt.Println("- Microsoft / Outlook / Hotmail / Office 365")
		fmt.Println("- Yahoo")
		fmt.Println("- standard SMTP/IMAP accounts with stored encrypted passwords")
	} else {
		fmt.Println("- unavailable in this build")
	}
	fmt.Println("Send reporting:")
	fmt.Println("- direct send prints the Message-ID, transport, and Sent-copy status")
	fmt.Println("- --verify <duration> confirms the Message-ID from the server before returning")
	fmt.Println("- reply threading via --in-reply-to / --references (direct send only)")
	fmt.Println("Fallback send path:")
	fmt.Println("- isolated Betterbird/Thunderbird automation via virtual display for unsupported providers")
	fmt.Println("- cannot set In-Reply-To/References; a threaded send is refused rather than sent unthreaded")
	fmt.Println("Backend selection:")
	fmt.Println("- default: SQLite under XDG state (~/.local/state/thunderbird-cli/...)")
	fmt.Println("- optional: set TB_STORE=postgres and TB_PG_DSN=... for PostgreSQL")
}

func runDoctor() error {
	fmt.Printf("thunderbird-cli %s\n\n", version)

	reportTbInstalls()

	root := detectThunderbirdRoot()
	fmt.Printf("Thunderbird root: %s\n", root)
	if _, err := os.Stat(filepath.Join(root, "profiles.ini")); err == nil {
		fmt.Println("Profiles file: OK")
	} else {
		fmt.Printf("Profiles file: missing (%v)\n", err)
	}

	app := newApp()
	if profiles, err := app.loadProfiles(); err == nil {
		fmt.Printf("Profiles detected: %d\n", len(profiles))
	} else {
		fmt.Printf("Profiles detected: error (%v)\n", err)
	}

	cmd := findMailCommand()
	fmt.Printf("Mail binary: %s\n", strings.Join(cmd, " "))
	fmt.Printf("Sync display path: %s\n", describeSyncDisplayPath(cmd))

	fmt.Printf("Selected backend: %s\n", selectedStoreBackend())
	switch selectedStoreBackend() {
	case storeBackendSQLite:
		path := defaultSQLitePath()
		fmt.Printf("SQLite path: %s\n", path)
		if err := mustMkdirParent(path); err != nil {
			fmt.Printf("SQLite parent dir: error (%v)\n", err)
		} else if sqliteRuntimeAvailable() {
			fmt.Println("SQLite runtime: OK")
		} else {
			fmt.Println("SQLite runtime: problem (unable to create temporary SQLite state file)")
		}
	case storeBackendPostgres:
		if strings.TrimSpace(os.Getenv("TB_PG_DSN")) == "" {
			fmt.Println("Postgres DSN: missing (set TB_PG_DSN)")
		} else {
			fmt.Println("Postgres DSN: configured")
		}
	}

	store, err := openStore()
	if err != nil {
		fmt.Printf("Cache backend open: error (%v)\n", err)
	} else {
		info := store.Info()
		fmt.Printf("Cache backend open: OK (%s: %s)\n", info.Backend, info.Location)
		store.Close()
	}

	ok, detail := detectNSSRuntime()
	if ok {
		fmt.Printf("NSS runtime: OK (%s)\n", detail)
	} else {
		fmt.Printf("NSS runtime: problem (%s)\n", detail)
	}

	if nssDirectSendCompiled() {
		fmt.Println("Direct send providers: Google, Microsoft, Yahoo, stored-password SMTP/IMAP")
	} else {
		fmt.Println("Direct send providers: unavailable in this build; fallback automation only")
	}
	fmt.Println("If direct send or cache setup is unavailable on a target machine, use `tb features` and README install notes.")
	return nil
}

// tbInstall is one `tb` binary found on this machine.
type tbInstall struct {
	Path    string
	Version string
	Running bool
	OnPath  bool
}

// findTbInstalls locates every `tb` a user could end up running: the one
// executing now, each one on PATH (in PATH order), and the conventional install
// directories. Interactive and non-interactive shells often have different
// PATHs, so "which tb" genuinely differs between an operator's terminal and
// `ssh host 'tb ...'` — this is what makes a stale copy hard to notice.
func findTbInstalls() []tbInstall {
	seen := map[string]*tbInstall{}
	var order []string

	add := func(path string, onPath bool) {
		if path == "" {
			return
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return
		}
		if existing, ok := seen[resolved]; ok {
			existing.OnPath = existing.OnPath || onPath
			return
		}
		seen[resolved] = &tbInstall{Path: resolved, OnPath: onPath}
		order = append(order, resolved)
	}

	running, _ := os.Executable()
	add(running, false)
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		add(filepath.Join(dir, "tb"), true)
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".local", "bin", "tb"), false)
	}
	add("/usr/local/bin/tb", false)

	runningResolved, _ := filepath.EvalSymlinks(running)
	out := make([]tbInstall, 0, len(order))
	for _, path := range order {
		entry := seen[path]
		entry.Running = path == runningResolved
		entry.Version = probeTbVersion(path, entry.Running)
		out = append(out, *entry)
	}
	return out
}

// probeTbVersion reads a binary's reported version. The running binary answers
// from memory rather than by re-executing itself.
func probeTbVersion(path string, running bool) string {
	if running {
		return version
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version").Output()
	if err != nil {
		return "unknown"
	}
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "thunderbird-cli"))
}

// reportTbInstalls prints the install picture and, when copies disagree, says
// plainly which one a shell will actually pick. A stale second copy silently
// shadowing the updated one is a real failure mode: `tb update` replaces only
// the binary it is running from, and a repo build lands in ./bin/tb.
func reportTbInstalls() {
	installs := findTbInstalls()
	if len(installs) == 0 {
		return
	}
	fmt.Printf("Installed binaries: %d\n", len(installs))
	versions := map[string]bool{}
	for _, in := range installs {
		markers := ""
		if in.Running {
			markers += " (running)"
		}
		if in.OnPath {
			markers += " (on PATH)"
		}
		fmt.Printf("  %s  %s%s\n", in.Version, in.Path, markers)
		versions[in.Version] = true
	}
	if len(versions) > 1 {
		var first string
		for _, in := range installs {
			if in.OnPath {
				first = in.Path
				break
			}
		}
		fmt.Println("  WARNING: these copies are different versions.")
		if first != "" {
			fmt.Printf("  A bare `tb` in this shell runs: %s\n", first)
		}
		fmt.Println("  Fix: keep one binary. `tb update` only replaces the copy it runs from,")
		fmt.Println("  and a repo build lands in ./bin/tb — copy it over the installed path:")
		fmt.Println("    sudo install -m 755 bin/tb /usr/local/bin/tb")
	}
}

// describeSyncDisplayPath answers the question that decides whether `--sync`
// can work at all from the current shell, so an operator learns it from
// `tb doctor` instead of from a sync that quietly read a stale cache.
func describeSyncDisplayPath(baseCmd []string) string {
	if guiSessionAvailable() {
		return "this shell already has a GUI session"
	}
	if session, ok := detectRunningMailSession(); ok {
		return fmt.Sprintf("no display in this shell; will join the %s (%s)", session.Source, session.display())
	}
	if _, err := exec.LookPath("Xvfb"); err == nil {
		return "no display and no running mail client; will start a temporary Xvfb display"
	}
	if mailCommandUsesFlatpak(baseCmd) {
		return "BLOCKED: Flatpak mail client, no GUI session, nothing running to join, and no Xvfb — start the mail client on the desktop, set THUNDERBIRD_BIN, or install Xvfb"
	}
	return "no display and no Xvfb; will fall back to -headless"
}

func detectNSSRuntime() (bool, string) {
	if !nssDirectSendCompiled() {
		return false, nssBuildDetail()
	}
	if runtime.GOOS != "linux" {
		return true, "build includes direct-send support; runtime library check only implemented on Linux"
	}
	exe, err := os.Executable()
	if err != nil {
		return false, err.Error()
	}
	ldd, err := exec.LookPath("ldd")
	if err != nil {
		return true, "ldd not available; binary is already running"
	}
	out, err := exec.Command(ldd, exe).CombinedOutput()
	if err != nil {
		return false, strings.TrimSpace(string(out))
	}
	text := string(out)
	if strings.Contains(text, "libnss3.so => not found") || strings.Contains(text, "libnspr4.so => not found") {
		return false, "required NSS shared libraries are missing"
	}
	return true, "linked NSS libraries resolved"
}

func checkForLatestRelease() (*releaseInfo, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", releaseRepoOwner, releaseRepoName), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("release lookup failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var rel releaseInfo
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}
