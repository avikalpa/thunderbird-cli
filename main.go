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
	log.Println("Usage: tb <domain> <command> [options]")
	log.Println("Domains:")
	log.Println("  mail    work with Thunderbird profiles/mailboxes (profiles/folders/recent/search/compose)")
	log.Println("  search  shorthand for: tb mail search ...")
	log.Println()
	log.Println("Examples:")
	log.Println("  tb mail profiles")
	log.Println("  tb mail folders --profile default")
	log.Println("  tb mail recent Inbox --limit 20")
	log.Println("  tb mail search \"court order\" --folder Inbox --limit 10")
	log.Println("  tb mail compose --to a@b --subject \"Update\" --body \"text\" --open")
}
