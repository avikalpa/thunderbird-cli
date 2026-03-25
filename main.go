package main

import (
	"log"
	"os"
)

func main() {
	log.SetFlags(0)
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
	log.Println("  search    shorthand for: tb mail search ...")
	log.Println()
	log.Println("Examples:")
	log.Println("  tb doctor")
	log.Println("  tb mail profiles")
	log.Println("  tb mail fetch --profile default --sync")
	log.Println("  tb mail search \"court order\" --limit 10")
	log.Println("  tb mail compose --to a@b --subject \"Update\" --body \"text\" --open")
	log.Println("  tb update --check")
}
