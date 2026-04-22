package main

import (
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
	version = "3.0.9"
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
	fmt.Println("Fallback send path:")
	fmt.Println("- isolated Betterbird/Thunderbird automation via virtual display for unsupported providers")
	fmt.Println("Backend selection:")
	fmt.Println("- default: SQLite under XDG state (~/.local/state/thunderbird-cli/...)")
	fmt.Println("- optional: set TB_STORE=postgres and TB_PG_DSN=... for PostgreSQL")
}

func runDoctor() error {
	fmt.Printf("thunderbird-cli %s\n\n", version)

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
