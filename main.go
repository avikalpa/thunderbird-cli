package main

import (
	"bufio"
	"log"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	log.SetFlags(0)
	loadDotenv()
	if len(os.Args) < 2 {
		usage()
		return
	}
	switch os.Args[1] {
	case "version":
		printVersion()
	case "features":
		printFeatures()
	case "doctor":
		if err := runDoctor(); err != nil {
			log.Fatalf("doctor: %v", err)
		}
	case "update":
		checkOnly := false
		if len(os.Args) > 2 {
			switch os.Args[2] {
			case "--check", "-c":
				checkOnly = true
			case "help", "-h", "--help":
				log.Println("Usage: tb update [--check]")
				return
			default:
				log.Fatalf("update: unknown argument %q", os.Args[2])
			}
		}
		if err := runUpdate(checkOnly); err != nil {
			log.Fatalf("update: %v", err)
		}
	case "mail":
		mailMain(os.Args[2:])
	case "list":
		// Convenience: allow `tb list ...` as shorthand for `tb mail recent ...`.
		mailMain(append([]string{"recent"}, os.Args[2:]...))
	case "tail":
		// Convenience: allow `tb tail ...` as shorthand for `tb mail unified ...`.
		mailMain(append([]string{"unified"}, os.Args[2:]...))
	case "head":
		// Convenience: allow `tb head ...` as shorthand for `tb mail unified --oldest ...`.
		mailMain(append([]string{"unified", "--oldest"}, os.Args[2:]...))
	case "read":
		// Convenience: allow `tb read ...` as shorthand for `tb mail show ...`.
		mailMain(append([]string{"show"}, os.Args[2:]...))
	case "find":
		// Convenience: allow `tb find ...` as shorthand for `tb mail search ...`.
		mailMain(append([]string{"search"}, os.Args[2:]...))
	case "search":
		// Convenience: allow `tb search ...` as shorthand for `tb mail search ...`.
		mailMain(append([]string{"search"}, os.Args[2:]...))
	case "help", "-h", "--help":
		usage()
	default:
		usage()
	}
}

func usage() {
	log.Println("Usage: tb <command> [options]")
	log.Println("Core commands:")
	log.Println("  version   show the installed build version")
	log.Println("  features  show backend and send capabilities for this build")
	log.Println("  doctor    inspect profile detection, cache backend, and runtime dependencies")
	log.Println("  update    update the installed binary from the latest GitHub release")
	log.Println("  mail      work with Thunderbird profiles/mailboxes (profiles/folders/recent/search/compose)")
	log.Println("  list      shorthand for: tb mail recent ...")
	log.Println("  tail      shorthand for: tb mail unified ...")
	log.Println("  head      shorthand for: tb mail unified --oldest ...")
	log.Println("  read      shorthand for: tb mail show ...")
	log.Println("  find      shorthand for: tb mail search ...")
	log.Println("  search    shorthand for: tb mail search ...")
	log.Println()
	log.Println("Examples:")
	log.Println("  tb doctor")
	log.Println("  tb mail profiles")
	log.Println("  tb list INBOX --account ops@example.org --limit 20 --raw")
	log.Println("  tb tail --limit 20 --raw --ignore-account alerts@example.org")
	log.Println("  tb read --message-id '<message-id>'")
	log.Println("  tb find \"court order\" --limit 10")
	log.Println("  tb mail fetch --profile default --sync")
	log.Println("  tb mail search \"court order\" --limit 10")
	log.Println("  tb mail compose --to a@b --subject \"Update\" --body \"text\" --open")
	log.Println("  tb update --check")
}

func loadDotenv() {
	seen := map[string]bool{}
	var paths []string
	add := func(path string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		paths = append(paths, path)
	}
	if wd, err := os.Getwd(); err == nil {
		add(filepath.Join(wd, ".env"))
	}
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		add(filepath.Join(dir, ".env"))
		add(filepath.Join(filepath.Dir(dir), ".env"))
	}
	for _, path := range paths {
		if err := loadEnvFile(path); err == nil {
			return
		}
	}
}

func loadEnvFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		val = strings.TrimSpace(val)
		val = strings.Trim(val, `"'`)
		_ = os.Setenv(key, val)
	}
	return scanner.Err()
}
