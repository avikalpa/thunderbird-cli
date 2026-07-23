package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-mbox"
	flag "github.com/spf13/pflag"
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"regexp"
	"text/tabwriter"
	"unicode"
	"unicode/utf8"
)

type App struct {
	Root string
}

type syncOptions struct {
	Timeout  time.Duration
	Headless bool
}

type Profile struct {
	Name         string
	Path         string
	AbsolutePath string
	IsRelative   bool
	Default      bool
}

type Mailbox struct {
	Name string
	Path string
	Size int64
}

type MailSummary struct {
	Profile   string
	Folder    string
	Subject   string
	From      string
	Date      string
	MessageID string
	Snippet   string
	When      time.Time
	Account   string
	Search    string
	FolderTag string
}

const (
	maxBodyBytes       = 1 << 20  // cap plain body read for simple messages
	maxMessageBytes    = 12 << 20 // cap total message read to avoid huge attachments
	maxPartBytes       = 2 << 20  // cap per MIME part when decoding bodies
	defaultMaxMessages = 0        // 0 = no cap; use --max-messages/--tail to bound scans
	defaultIndexTail   = 0        // 0 = full history; set --tail to bound indexing if desired
)

type ingestOptions struct {
	accountEmail string
	folderLike   string
	syncFirst    bool
	prune        bool
	maxMessages  int
	tailCount    int
	fullRescan   bool
}

// folderFingerprint records what tb has already ingested from a folder.
//
// The v2 form carries the ingested size as well as the mtime, which is what
// makes append-only incremental ingest possible. Older v1 values ("mtime:size")
// are treated as "unknown size" and simply force one full rescan.
func folderFingerprint(modTime time.Time, size int64) string {
	return fmt.Sprintf("v2|%d|%d", modTime.UnixNano(), size)
}

// fingerprintIngestedSize reports the size tb last ingested to, if known.
func fingerprintIngestedSize(fingerprint string) (int64, bool) {
	parts := strings.Split(fingerprint, "|")
	if len(parts) != 3 || parts[0] != "v2" {
		return 0, false
	}
	size, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || size < 0 {
		return 0, false
	}
	return size, true
}

// mboxAppendOffset decides whether a folder only grew, so ingest can resume
// from where it stopped instead of re-reading the file.
//
// It requires the file to be larger than last time AND the previous end to sit
// exactly on an mbox message separator. A compacted or rewritten mbox will not
// satisfy the second condition, and falls back to a full rescan.
func mboxAppendOffset(path string, prevSize, currentSize int64) (int64, bool) {
	if prevSize <= 0 || currentSize <= prevSize {
		return 0, false
	}
	f, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer f.Close()
	if _, err := f.Seek(prevSize, io.SeekStart); err != nil {
		return 0, false
	}
	head := make([]byte, 5)
	if _, err := io.ReadFull(f, head); err != nil {
		return 0, false
	}
	if string(head) != "From " {
		return 0, false
	}
	return prevSize, true
}

func fingerprintKey(profile string, path string) string {
	return fmt.Sprintf("fp|%s|%s", profile, path)
}

func mailMain(args []string) {
	if len(args) == 0 {
		mailUsage()
		return
	}
	app := newApp()
	// Accept flags before OR after positionals (see reorderArgs).
	args = append([]string{args[0]}, reorderArgs(args[1:])...)
	switch args[0] {
	case "profiles":
		cmd := flag.NewFlagSet("profiles", flag.ExitOnError)
		cmd.Parse(args[1:])
		if err := app.printProfiles(); err != nil {
			log.Fatalf("profiles: %v", err)
		}
	case "read":
		// Alias for show.
		mailMain(append([]string{"show"}, args[1:]...))
		return
	case "send":
		// Alias for compose.
		mailMain(append([]string{"compose"}, args[1:]...))
		return
	case "index":
		cmd := flag.NewFlagSet("index", flag.ExitOnError)
		profileName := cmd.String("profile", "", "profile name or path")
		folderLike := cmd.String("folder", "", "restrict to folders containing this name")
		account := cmd.String("account", "", "filter by account email")
		accountShort := cmd.String("ac", "", "alias for --account")
		tailCount := cmd.Int("tail", defaultIndexTail, "keep only last N messages per folder (0 = all)")
		cmd.Parse(args[1:])
		acct := *account
		if acct == "" {
			acct = *accountShort
		}
		if err := app.buildIndex(*profileName, *folderLike, acct, *tailCount); err != nil {
			log.Fatalf("index: %v", err)
		}
	case "folders":
		cmd := flag.NewFlagSet("folders", flag.ExitOnError)
		profileName := cmd.String("profile", "", "profile name or path")
		cmd.Parse(args[1:])
		if err := app.printFolders(*profileName); err != nil {
			log.Fatalf("folders: %v", err)
		}
	case "recent":
		cmd := flag.NewFlagSet("recent", flag.ExitOnError)
		profileName := cmd.String("profile", "", "profile name or path")
		limit := cmd.Int("limit", 20, "max messages to show")
		query := cmd.String("query", "", "substring filter against subject/from/body")
		account := cmd.String("account", "", "filter by account email")
		accountShort := cmd.String("ac", "", "alias for --account")
		raw := cmd.Bool("raw", false, "plain output with account and message-id; better for scripts")
		syncFirst := cmd.Bool("sync", false, "run Thunderbird/Betterbird sync before reading the mailbox tail")
		cmd.Parse(args[1:])
		pos := cmd.Args()
		folder := ""
		if len(pos) > 0 {
			folder = pos[0]
		}
		acct := *account
		if acct == "" {
			acct = *accountShort
		}
		if err := app.recent(folder, *profileName, acct, *limit, *query, *raw, *syncFirst); err != nil {
			log.Fatalf("recent: %v", err)
		}
	case "unified":
		cmd := flag.NewFlagSet("unified", flag.ExitOnError)
		profileName := cmd.String("profile", "", "profile name or path")
		limit := cmd.Int("limit", 20, "max messages to show")
		query := cmd.String("query", "", "substring filter against subject/from/body")
		account := cmd.String("account", "", "filter by account email")
		accountShort := cmd.String("ac", "", "alias for --account")
		raw := cmd.Bool("raw", false, "plain output with account and message-id; better for scripts")
		syncFirst := cmd.Bool("sync", false, "run Thunderbird/Betterbird sync before reading the unified inbox tail")
		oldest := cmd.Bool("oldest", false, "show oldest matches first instead of newest")
		ignoreAccountsCSV := cmd.String("ignore-account", "", "comma-separated account emails to ignore")
		ignoreFoldersCSV := cmd.String("ignore-folder", "", "comma-separated folder substrings to ignore")
		cmd.Parse(args[1:])
		acct := *account
		if acct == "" {
			acct = *accountShort
		}
		if err := app.unifiedInbox(*profileName, acct, *limit, *query, *raw, *syncFirst, *oldest, splitCSV(*ignoreAccountsCSV), splitCSV(*ignoreFoldersCSV)); err != nil {
			log.Fatalf("unified: %v", err)
		}
	case "search":
		cmd := flag.NewFlagSet("search", flag.ExitOnError)
		profileName := cmd.String("profile", "", "profile name or path")
		folderLike := cmd.String("folder", "", "restrict to folders containing this name")
		limit := cmd.Int("limit", 25, "max results across folders")
		since := cmd.String("since", "", "only include messages on/after YYYY-MM-DD")
		sinceShort := cmd.String("ds", "", "alias for --since")
		till := cmd.String("till", "", "only include messages on/before YYYY-MM-DD")
		tillShort := cmd.String("dt", "", "alias for --till")
		account := cmd.String("account", "", "filter by account email")
		accountShort := cmd.String("ac", "", "alias for --account")
		raw := cmd.Bool("raw", false, "plain output (no table; LLM-friendly)")
		legacyNoFancy := cmd.Bool("no-fancy", false, "deprecated: use --raw")
		refresh := cmd.Bool("refresh", false, "incremental refresh (ingest changed folders) before searching")
		fullRescan := cmd.Bool("full-rescan", false, "force full rescan into the configured cache backend before searching")
		fuzzy := cmd.Bool("fuzzy", false, "fuzzy token match (all tokens must appear)")
		cmd.Parse(args[1:])
		pos := cmd.Args()
		if len(pos) < 1 {
			log.Fatalf("search: query required")
		}
		useRaw := *raw || *legacyNoFancy
		var sinceTime time.Time
		if *since != "" {
			t, err := time.Parse("2006-01-02", *since)
			if err != nil {
				log.Fatalf("search: bad --since date (use YYYY-MM-DD): %v", err)
			}
			sinceTime = t
		}
		if sinceTime.IsZero() && *sinceShort != "" {
			t, err := time.Parse("2006-01-02", *sinceShort)
			if err != nil {
				log.Fatalf("search: bad --ds date (use YYYY-MM-DD): %v", err)
			}
			sinceTime = t
		}
		var tillTime time.Time
		if *till != "" {
			t, err := time.Parse("2006-01-02", *till)
			if err != nil {
				log.Fatalf("search: bad --till date (use YYYY-MM-DD): %v", err)
			}
			tillTime = t.Add(24 * time.Hour) // inclusive
		}
		if tillTime.IsZero() && *tillShort != "" {
			t, err := time.Parse("2006-01-02", *tillShort)
			if err != nil {
				log.Fatalf("search: bad --dt date (use YYYY-MM-DD): %v", err)
			}
			tillTime = t.Add(24 * time.Hour)
		}
		acct := *account
		if acct == "" {
			acct = *accountShort
		}
		if err := app.search(pos[0], *profileName, *folderLike, acct, *limit, useRaw, sinceTime, tillTime, *refresh, *fullRescan, *fuzzy); err != nil {
			log.Fatalf("search: %v", err)
		}
	case "compose":
		cmd := flag.NewFlagSet("compose", flag.ExitOnError)
		profileName := cmd.String("profile", "", "profile name or path (defaults to Thunderbird/Betterbird default)")
		to := cmd.String("to", "", "comma-separated recipients")
		cc := cmd.String("cc", "", "cc recipients")
		from := cmd.String("from", "", "sender email/identity")
		subject := cmd.String("subject", "", "subject")
		body := cmd.String("body", "", "body text")
		bodyFile := cmd.String("body-file", "", "read the body from this file ('-' reads stdin); avoids shell quoting for long messages")
		inReplyTo := cmd.String("in-reply-to", "", "Message-ID this message replies to; sets In-Reply-To (and References if unset)")
		references := cmd.String("references", "", "comma/space separated Message-ID chain for the References header")
		openComposer := cmd.Bool("open", true, "open Thunderbird compose window")
		sendNow := cmd.Bool("send", false, "auto-send headlessly via an isolated Betterbird/Thunderbird profile clone")
		verify := cmd.Duration("verify", 0, "after --send, poll the Sent mailbox this long to confirm the Message-ID landed (0 = skip)")
		cmd.Parse(args[1:])
		if *to == "" {
			log.Fatalf("compose: --to is required")
		}
		if !*openComposer && !*sendNow {
			log.Fatalf("compose: nothing to do (set --open or --send)")
		}
		messageBody := *body
		if *bodyFile != "" {
			if *body != "" {
				log.Fatalf("compose: use either --body or --body-file, not both")
			}
			loaded, err := readBodyFile(*bodyFile)
			if err != nil {
				log.Fatalf("compose: %v", err)
			}
			messageBody = loaded
		}
		req := composeRequest{
			To:         *to,
			Cc:         *cc,
			From:       *from,
			Subject:    *subject,
			Body:       messageBody,
			InReplyTo:  *inReplyTo,
			References: *references,
		}
		if err := app.compose(*profileName, req, *openComposer, *sendNow, *verify); err != nil {
			log.Fatalf("compose: %v", err)
		}
	case "sentcheck":
		cmd := flag.NewFlagSet("sentcheck", flag.ExitOnError)
		profileName := cmd.String("profile", "", "profile name or path")
		from := cmd.String("from", "", "sender identity/account email")
		subject := cmd.String("subject", "", "exact or partial subject to match in Sent")
		messageID := cmd.String("message-id", "", "exact Message-ID to match in Sent")
		mailbox := cmd.String("mailbox", "", "sent mailbox override")
		wait := cmd.Duration("wait", 15*time.Second, "how long to poll IMAP for the sent copy")
		limit := cmd.Int("limit", 1, "maximum matching messages to print")
		cmd.Parse(args[1:])
		if *subject == "" && *messageID == "" {
			log.Fatalf("sentcheck: one of --subject or --message-id is required")
		}
		if err := app.sentcheck(*profileName, *from, *subject, *messageID, *mailbox, *wait, *limit); err != nil {
			log.Fatalf("sentcheck: %v", err)
		}
	case "authcheck":
		cmd := flag.NewFlagSet("authcheck", flag.ExitOnError)
		profileName := cmd.String("profile", "", "profile name or path")
		from := cmd.String("from", "", "sender identity email")
		to := cmd.String("to", "", "recipient address to send the test to")
		readAs := cmd.String("read-as", "", "account email to poll for the delivered message (defaults to --to)")
		subject := cmd.String("subject", "", "override subject; defaults to an auto-generated authcheck subject")
		body := cmd.String("body", "", "override message body")
		wait := cmd.Duration("wait", 2*time.Minute, "how long to wait for delivery before giving up")
		mailboxes := cmd.String("mailboxes", "", "comma-separated mailbox list to poll; defaults based on the reader account provider")
		cmd.Parse(args[1:])
		var boxes []string
		for _, box := range strings.Split(*mailboxes, ",") {
			box = strings.TrimSpace(box)
			if box != "" {
				boxes = append(boxes, box)
			}
		}
		if err := app.authcheck(*profileName, *from, *to, *readAs, *subject, *body, *wait, boxes); err != nil {
			log.Fatalf("authcheck: %v", err)
		}
	case "move":
		cmd := flag.NewFlagSet("move", flag.ExitOnError)
		profileName := cmd.String("profile", "", "profile name or path")
		account := cmd.String("account", "", "account email to operate against")
		accountShort := cmd.String("ac", "", "alias for --account")
		sourceMailbox := cmd.String("source-mailbox", "", "source mailbox/folder name")
		destMailbox := cmd.String("dest-mailbox", "", "destination mailbox/folder name")
		subject := cmd.String("subject", "", "exact subject to match")
		messageID := cmd.String("message-id", "", "exact Message-ID to match")
		limit := cmd.Int("limit", 1, "maximum matching messages to move")
		cmd.Parse(args[1:])
		acct := *account
		if acct == "" {
			acct = *accountShort
		}
		if err := app.moveMail(*profileName, acct, *sourceMailbox, *destMailbox, *subject, *messageID, *limit); err != nil {
			log.Fatalf("move: %v", err)
		}
	case "fetch":
		cmd := flag.NewFlagSet("fetch", flag.ExitOnError)
		profileName := cmd.String("profile", "", "profile name or path")
		folderLike := cmd.String("folder", "", "restrict to folders containing this name")
		account := cmd.String("account", "", "filter by account email")
		accountShort := cmd.String("ac", "", "alias for --account")
		syncFirst := cmd.Bool("sync", false, "run Thunderbird/Betterbird sync before ingest")
		prune := cmd.Bool("prune", false, "delete DB rows for this profile that are no longer present on disk")
		fullRescan := cmd.Bool("full", false, "force full rescan instead of incremental ingest")
		maxScan := cmd.Int("max-messages", 0, "optional cap per folder during ingest (0 = all)")
		tailCount := cmd.Int("tail", 0, "keep only last N messages per folder during ingest (0 = all)")
		cmd.Parse(args[1:])
		acct := *account
		if acct == "" {
			acct = *accountShort
		}
		if err := app.fetch(*profileName, *folderLike, acct, *syncFirst, *prune, *fullRescan, *maxScan, *tailCount); err != nil {
			log.Fatalf("fetch: %v", err)
		}
	case "sync":
		cmd := flag.NewFlagSet("sync", flag.ExitOnError)
		profileName := cmd.String("profile", "", "profile name or path")
		timeout := cmd.Duration("timeout", syncTimeout(), "how long to let Thunderbird/Betterbird fetch mail")
		headless := cmd.Bool("headless", false, "force Thunderbird/Betterbird -headless mode")
		cmd.Parse(args[1:])
		if err := app.sync(*profileName, syncOptions{Timeout: *timeout, Headless: *headless}); err != nil {
			log.Fatalf("sync: %v", err)
		}
	case "help", "-h", "--help":
		mailUsage()
	case "show":
		cmd := flag.NewFlagSet("show", flag.ExitOnError)
		profileName := cmd.String("profile", "", "profile name or path")
		folderLike := cmd.String("folder", "", "folder name/substring to search")
		query := cmd.String("query", "", "substring match against subject/from/body")
		messageID := cmd.String("message-id", "", "exact Message-ID match; more reliable than substring search when you already have a recent/raw hit")
		limit := cmd.Int("limit", 1, "max messages to display")
		account := cmd.String("account", "", "filter by account email")
		accountShort := cmd.String("ac", "", "alias for --account")
		thread := cmd.Bool("thread", false, "if set, show entire thread (same subject) after first match")
		cmd.Parse(args[1:])
		if *messageID == "" && (*folderLike == "" || *query == "") {
			log.Fatalf("show: either --message-id, or both --folder and --query, are required")
		}
		acct := *account
		if acct == "" {
			acct = *accountShort
		}
		if err := app.showMail(*profileName, *folderLike, *query, *messageID, acct, *limit, *thread); err != nil {
			log.Fatalf("show: %v", err)
		}
	default:
		mailUsage()
	}
}

func mailUsage() {
	log.Println("Usage: tb mail <command> [options]")
	log.Println("Commands:")
	log.Println("  profiles                             list Thunderbird profiles from profiles.ini")
	log.Println("  folders [--profile name]             list mailboxes for a profile")
	log.Println("  recent [folder] [--profile p] [--account/--ac email] [--limit N] [--query q] [--raw] [--sync]  show newest messages from a folder (defaults to the inbox) before narrowing search terms")
	log.Println("  unified [--profile p] [--account/--ac email] [--limit N] [--query q] [--raw] [--sync] [--oldest] [--ignore-account a,b] [--ignore-folder x,y]  show a unified inbox list across accounts")
	log.Println("  search <query> [--since/--ds YYYY-MM-DD] [--till/--dt YYYY-MM-DD] [--account/--ac email] [--folder name] [--refresh] [--full-rescan] [--raw] [--fuzzy]")
	log.Println("  index [--profile p] [--folder f] [--account/--ac email] [--tail N]   prebuild cache for faster search")
	log.Println("  fetch [--profile p] [--sync] [--prune] [--full] [--account/--ac email] [--folder f] [--max-messages N] [--tail N]  ingest mail into the configured cache backend")
	log.Println("  sync [--profile p] [--timeout 90s] [--headless]  run Thunderbird/Betterbird sync without requiring a cache backend")
	log.Println("  show/read [--folder <name> --query <text> | --message-id <id>] [--profile p] [--account/--ac email] [--limit N] [--thread]  print full messages or a whole thread")
	log.Println("  compose/send --to ... [--from a@b] [--subject s] [--body t | --body-file f] [--in-reply-to <id>] [--references <ids>] [--send --open=false] [--verify 30s]  open or headlessly send mail")
	log.Println("  sentcheck [--from a@b] [--subject s | --message-id id] [--profile p] [--mailbox Sent] [--wait 15s] [--limit N]  verify sent mail online via IMAP")
	log.Println("  authcheck --from a@b --to c@d [--read-as x@y] [--wait 2m] [--mailboxes m1,m2]  send a test and print authentication headers from the receiving account")
	log.Println("  move [--profile p] --account a@b --source-mailbox Junk --dest-mailbox INBOX [--message-id <id> | --subject s] [--limit N]  move matching remote IMAP mail")
}

func newApp() *App {
	root := os.Getenv("THUNDERBIRD_HOME")
	if strings.TrimSpace(root) != "" {
		return &App{Root: root}
	}
	return &App{Root: detectThunderbirdRoot()}
}

func detectThunderbirdRoot() string {
	home := os.Getenv("HOME")
	candidates := []string{
		filepath.Join(home, ".var", "app", "eu.betterbird.Betterbird", ".thunderbird"),
		filepath.Join(home, ".var", "app", "org.mozilla.Thunderbird", ".thunderbird"),
		filepath.Join(home, ".thunderbird"),
	}

	for _, root := range candidates {
		if _, err := os.Stat(filepath.Join(root, "profiles.ini")); err == nil {
			return root
		}
	}
	for _, root := range candidates {
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			return root
		}
	}

	return candidates[0]
}

func indexPath(profile Profile) string {
	return filepath.Join(profile.AbsolutePath, ".tb-index.json")
}

func (a *App) printProfiles() error {
	profiles, err := a.loadProfiles()
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		fmt.Printf("No profiles found under %s\n", a.Root)
		return nil
	}
	fmt.Printf("%-12s %-8s %s\n", "Name", "Default", "Path")
	for _, p := range profiles {
		def := ""
		if p.Default {
			def = "yes"
		}
		fmt.Printf("%-12s %-8s %s\n", p.Name, def, p.AbsolutePath)
	}
	return nil
}

func (a *App) printFolders(profileName string) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	boxes, err := a.listMailboxes(profile)
	if err != nil {
		return err
	}
	if len(boxes) == 0 {
		fmt.Printf("No mailboxes found under %s\n", profile.AbsolutePath)
		return nil
	}
	fmt.Printf("Mailboxes for %s (%s):\n", profile.Name, profile.AbsolutePath)
	for _, b := range boxes {
		fmt.Printf("- %s [%s]\n", b.Name, byteSize(b.Size))
	}
	return nil
}

func (a *App) recent(folder, profileName, accountEmail string, limit int, query string, raw bool, syncFirst bool) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	accountEmail = strings.ToLower(strings.TrimSpace(accountEmail))
	if syncFirst {
		// --sync was asked for explicitly. Continuing on failure would answer
		// from a stale cache while looking like fresh mail, which already
		// produced a false "no such email" conclusion.
		if err := a.syncProfile(profile); err != nil {
			return fmt.Errorf("sync profile %s: %w (re-run without --sync to search the existing cache)", profile.Name, err)
		}
	}
	boxes, err := a.listMailboxes(profile)
	if err != nil {
		return err
	}
	dirToAccount, err := a.accountDirIndex(profile)
	if err != nil {
		return fmt.Errorf("account index: %w", err)
	}
	boxes, err = filterMailboxes(boxes, folder, accountEmail, dirToAccount)
	if err != nil {
		return err
	}
	scope := folder
	if folder == "" {
		// No folder given: read the inbox(es) rather than refusing. Requiring a
		// positional folder made the most common lookup fail on first try.
		inboxes := inboxMailboxes(boxes)
		if len(inboxes) == 0 {
			return fmt.Errorf("no inbox folder found; name a folder explicitly (available: %s)", describeFolders(boxes, 8))
		}
		boxes = inboxes
		scope = "inbox"
	}
	var messages []MailSummary
	var skips skipTracker
	for _, box := range boxes {
		targetAccount := accountEmail
		if targetAccount == "" {
			targetAccount = accountForPath(box.Path, dirToAccount)
		}
		boxMessages, err := readMailboxRecent(box, limit, query)
		if err != nil {
			skips.note("recent", box.Name, err)
			continue
		}
		decorateMessages(boxMessages, profile.Name, targetAccount)
		messages = append(messages, boxMessages...)
	}
	skips.report()
	if len(messages) == 0 {
		// Name the folders actually read: an empty result must never be
		// confusable with having read the wrong mailbox.
		fmt.Printf("No messages found in %s (%d folder(s): %s).\n", scope, len(boxes), describeFolders(boxes, 6))
		return nil
	}
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].When.IsZero() && messages[j].When.IsZero() {
			return messages[i].Date > messages[j].Date
		}
		if messages[i].When.IsZero() {
			return false
		}
		if messages[j].When.IsZero() {
			return true
		}
		return messages[i].When.After(messages[j].When)
	})
	if limit > 0 && len(messages) > limit {
		messages = messages[:limit]
	}
	if raw {
		for _, m := range messages {
			date := m.Date
			if !m.When.IsZero() {
				date = m.When.Format("2006-01-02 15:04")
			}
			fmt.Printf("%s | %s | %s | %s | %s | %s | %s\n",
				date,
				truncate(m.Account, 32),
				truncate(m.Folder, 36),
				truncate(m.From, 40),
				truncate(m.Subject, 60),
				truncate(m.MessageID, 72),
				truncate(m.Snippet, 120))
		}
		return nil
	}
	fmt.Printf("Recent from %s (profile %s, %d folder(s)):\n", scope, profile.Name, len(boxes))
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "DATE\tACCOUNT\tFOLDER\tFROM\tSUBJECT\tSNIPPET\n")
	fmt.Fprintf(w, "----\t-------\t------\t----\t-------\t-------\n")
	for _, m := range messages {
		date := m.Date
		if !m.When.IsZero() {
			date = m.When.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			date,
			truncate(m.Account, 24),
			truncate(m.Folder, 24),
			truncate(m.From, 40),
			truncate(m.Subject, 60),
			truncate(m.Snippet, 100))
	}
	return w.Flush()
}

func (a *App) unifiedInbox(profileName, accountEmail string, limit int, query string, raw bool, syncFirst bool, oldest bool, ignoreAccounts []string, ignoreFolders []string) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	accountEmail = strings.ToLower(strings.TrimSpace(accountEmail))
	ignoreAccounts = normalizeMailFilters(ignoreAccounts)
	ignoreFolders = normalizeMailFilters(ignoreFolders)

	if syncFirst {
		// --sync was asked for explicitly. Continuing on failure would answer
		// from a stale cache while looking like fresh mail, which already
		// produced a false "no such email" conclusion.
		if err := a.syncProfile(profile); err != nil {
			return fmt.Errorf("sync profile %s: %w (re-run without --sync to search the existing cache)", profile.Name, err)
		}
	}

	boxes, err := a.listMailboxes(profile)
	if err != nil {
		return err
	}
	dirToAccount, err := a.accountDirIndex(profile)
	if err != nil {
		return fmt.Errorf("account index: %w", err)
	}
	boxes, err = filterMailboxes(boxes, "", accountEmail, dirToAccount)
	if err != nil {
		return err
	}

	boxes = filterUnifiedInboxMailboxes(boxes, dirToAccount, ignoreAccounts, ignoreFolders)
	if len(boxes) == 0 {
		return fmt.Errorf("no inbox folders matched the current filters")
	}

	perBoxLimit := limit
	if perBoxLimit <= 0 {
		perBoxLimit = 50
	}
	if perBoxLimit < 50 {
		perBoxLimit = 50
	}

	var messages []MailSummary
	for _, box := range boxes {
		targetAccount := accountForPath(box.Path, dirToAccount)
		boxMessages, err := readMailboxRecent(box, perBoxLimit, query)
		if err != nil {
			log.Printf("warn: unified %s: %v", box.Name, err)
			continue
		}
		decorateMessages(boxMessages, profile.Name, targetAccount)
		messages = append(messages, boxMessages...)
	}
	if len(messages) == 0 {
		fmt.Println("No messages found.")
		return nil
	}

	sort.Slice(messages, func(i, j int) bool {
		if messages[i].When.IsZero() && messages[j].When.IsZero() {
			if oldest {
				return messages[i].Date < messages[j].Date
			}
			return messages[i].Date > messages[j].Date
		}
		if messages[i].When.IsZero() {
			return oldest
		}
		if messages[j].When.IsZero() {
			return !oldest
		}
		if oldest {
			return messages[i].When.Before(messages[j].When)
		}
		return messages[i].When.After(messages[j].When)
	})
	if limit > 0 && len(messages) > limit {
		messages = messages[:limit]
	}

	if raw {
		for _, m := range messages {
			date := m.Date
			if !m.When.IsZero() {
				date = m.When.Format("2006-01-02 15:04")
			}
			fmt.Printf("%s | %s | %s | %s | %s | %s | %s\n",
				date,
				truncate(m.Account, 32),
				truncate(m.Folder, 36),
				truncate(m.From, 40),
				truncate(m.Subject, 60),
				truncate(m.MessageID, 72),
				truncate(m.Snippet, 120))
		}
		return nil
	}

	title := "Unified inbox"
	if oldest {
		title = "Unified inbox (oldest first)"
	}
	fmt.Printf("%s (profile %s):\n", title, profile.Name)
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "DATE\tACCOUNT\tFOLDER\tFROM\tSUBJECT\tSNIPPET\n")
	fmt.Fprintf(w, "----\t-------\t------\t----\t-------\t-------\n")
	for _, m := range messages {
		date := m.Date
		if !m.When.IsZero() {
			date = m.When.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			date,
			truncate(m.Account, 24),
			truncate(m.Folder, 24),
			truncate(m.From, 40),
			truncate(m.Subject, 60),
			truncate(m.Snippet, 100))
	}
	return w.Flush()
}

func (a *App) accountDirIndex(profile Profile) (map[string]string, error) {
	dirToAccount := map[string]string{}
	idx, err := a.loadAccountDirIndex(profile)
	if err != nil {
		return dirToAccount, err
	}
	for acct, dirs := range idx {
		for _, d := range dirs {
			dirToAccount[d] = acct
		}
	}
	return dirToAccount, nil
}

func filterMailboxes(boxes []Mailbox, folderLike, accountEmail string, dirToAccount map[string]string) ([]Mailbox, error) {
	if accountEmail != "" {
		var scoped []Mailbox
		for _, b := range boxes {
			if accountForPath(b.Path, dirToAccount) == accountEmail {
				scoped = append(scoped, b)
			}
		}
		boxes = scoped
		if len(boxes) == 0 {
			return nil, fmt.Errorf("no folders for account %s", accountEmail)
		}
	}
	if folderLike == "" {
		return boxes, nil
	}
	needle := strings.ToLower(folderLike)
	var filtered []Mailbox
	for _, b := range boxes {
		if strings.Contains(strings.ToLower(b.Name), needle) || strings.Contains(strings.ToLower(filepath.Base(b.Name)), needle) {
			filtered = append(filtered, b)
		}
	}
	if len(filtered) == 0 {
		// Substring matching fails on real folder names an operator half
		// remembers: "Sent Mail" against "Sent Items", "acme inbox" against
		// "ImapMail/mail.acme-1.example/INBOX". Fall back to requiring every token
		// somewhere in the path before declaring the folder absent.
		filtered = fuzzyMatchMailboxes(boxes, needle)
	}
	if len(filtered) == 0 {
		scope := ""
		if accountEmail != "" {
			scope = fmt.Sprintf(" in account %s", accountEmail)
		}
		return nil, fmt.Errorf("no folders match %q%s; available: %s", folderLike, scope, describeFolders(boxes, 8))
	}
	return filtered, nil
}

// fuzzyMatchMailboxes matches a mailbox when every token of the query appears
// somewhere in its path, in any order.
func fuzzyMatchMailboxes(boxes []Mailbox, needle string) []Mailbox {
	tokens := strings.FieldsFunc(needle, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(tokens) == 0 {
		return nil
	}
	var filtered []Mailbox
	for _, b := range boxes {
		name := strings.ToLower(b.Name)
		matchedAll := true
		for _, token := range tokens {
			if !strings.Contains(name, token) {
				matchedAll = false
				break
			}
		}
		if matchedAll {
			filtered = append(filtered, b)
		}
	}
	return filtered
}

// describeFolders renders a short folder list for messages that would otherwise
// leave the operator guessing which mailboxes were involved.
func describeFolders(boxes []Mailbox, max int) string {
	if len(boxes) == 0 {
		return "(none)"
	}
	names := make([]string, 0, len(boxes))
	for i, b := range boxes {
		if i >= max {
			names = append(names, fmt.Sprintf("... (+%d more)", len(boxes)-max))
			break
		}
		names = append(names, b.Name)
	}
	return strings.Join(names, ", ")
}

// skipTracker records folders that could not be read, deduplicating by folder.
//
// Thunderbird keeps non-mbox files (notably Yahoo's Trash) in the mail tree.
// A single such file used to emit one "invalid mbox format" warning per record
// — over twelve thousand lines for one folder — which trained operators to
// ignore tb's warnings entirely. Benign format failures are now summarised
// once; TB_VERBOSE=1 shows one line per affected folder.
type skipTracker struct {
	benign map[string]bool
	warned map[string]bool
}

// note records a per-mailbox read failure. Real errors are still reported, but
// only once per folder so a corrupt file cannot drown the output either.
func (s *skipTracker) note(scope, name string, err error) {
	first := !s.warned[name]
	if s.warned == nil {
		s.warned = map[string]bool{}
	}
	s.warned[name] = true

	if !errors.Is(err, mbox.ErrInvalidFormat) {
		if first {
			log.Printf("warn: %s %s: %v", scope, name, err)
		}
		return
	}
	if s.benign == nil {
		s.benign = map[string]bool{}
	}
	s.benign[name] = true
	if verboseEnabled() && first {
		log.Printf("warn: %s %s: %v", scope, name, err)
	}
}

// report prints the one-line summary that keeps suppression honest: the
// operator still learns that folders were skipped, and how to see which.
func (s *skipTracker) report() {
	if len(s.benign) == 0 || verboseEnabled() {
		return
	}
	log.Printf("info: skipped %d folder(s) that are not valid mbox files (set TB_VERBOSE=1 to list them)", len(s.benign))
}

func verboseEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TB_VERBOSE"))) {
	case "", "0", "false", "no":
		return false
	}
	return true
}

// inboxMailboxes keeps only inbox folders, used when a command defaults to
// "the inbox" rather than requiring an explicit folder name.
func inboxMailboxes(boxes []Mailbox) []Mailbox {
	var filtered []Mailbox
	for _, b := range boxes {
		if isUnifiedInboxMailbox(b) {
			filtered = append(filtered, b)
		}
	}
	return filtered
}

func filterUnifiedInboxMailboxes(boxes []Mailbox, dirToAccount map[string]string, ignoreAccounts []string, ignoreFolders []string) []Mailbox {
	var filtered []Mailbox
	for _, b := range boxes {
		if !isUnifiedInboxMailbox(b) {
			continue
		}
		account := strings.ToLower(strings.TrimSpace(accountForPath(b.Path, dirToAccount)))
		if account != "" && containsMailFilter(ignoreAccounts, account) {
			continue
		}
		name := strings.ToLower(b.Name)
		if containsMailFilter(ignoreFolders, name) {
			continue
		}
		filtered = append(filtered, b)
	}
	return filtered
}

func isUnifiedInboxMailbox(box Mailbox) bool {
	base := strings.ToLower(filepath.Base(box.Name))
	switch base {
	case "inbox":
		return true
	case "junk", "junk mail", "spam", "trash", "sent", "drafts", "archives":
		return false
	}
	name := strings.ToLower(box.Name)
	return strings.HasSuffix(name, "/inbox") || strings.Contains(name, "/inbox/")
}

func normalizeMailFilters(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		v = strings.ToLower(strings.TrimSpace(v))
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

func containsMailFilter(filters []string, value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	for _, filter := range filters {
		if filter == "" {
			continue
		}
		if value == filter || strings.Contains(value, filter) {
			return true
		}
	}
	return false
}

func matchMessage(summary MailSummary, bodyText, queryLower, messageID string) bool {
	if messageID != "" {
		return strings.TrimSpace(summary.MessageID) == strings.TrimSpace(messageID)
	}
	blob := strings.ToLower(strings.Join([]string{summary.Subject, summary.From, bodyText}, " "))
	return strings.Contains(blob, queryLower)
}

func (a *App) search(query, profileName, folderLike, accountEmail string, limit int, raw bool, since, till time.Time, refresh bool, fullRescan bool, fuzzy bool) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	accountEmail = strings.ToLower(strings.TrimSpace(accountEmail))
	store, err := openStore()
	if err != nil {
		return fmt.Errorf("open search store: %w", err)
	}
	defer store.Close()

	ctx := context.Background()
	if refresh && fullRescan {
		log.Printf("info: full rescan requested for profile %s", profile.Name)
	}

	// Auto-refresh: pick up newly arrived mail without the caller having to
	// remember --refresh. Window via TB_AUTO_REFRESH_MINUTES (0 disables).
	if !refresh && !fullRescan && autoRefreshWindow() > 0 {
		if ts, mErr := store.GetMeta(ctx, fmt.Sprintf("last_scan.%s", profile.Name)); mErr == nil && ts != "" {
			if last, pErr := time.Parse(time.RFC3339, ts); pErr == nil && time.Since(last) > autoRefreshWindow() {
				refresh = true
			}
		}
	}

	cacheCount, countErr := store.CountMessages(ctx, profile.Name)
	if countErr == nil && cacheCount == 0 && !refresh && !fullRescan {
		log.Printf("info: cache empty for profile %s, scanning mailbox files directly", profile.Name)
		return a.searchDirect(profile, query, folderLike, accountEmail, limit, raw, since, till, fuzzy)
	}

	if refresh || fullRescan {
		log.Printf("info: refreshing cache from profile %s", profile.Name)
		if err := a.ingestProfile(ctx, store, profile, ingestOptions{
			accountEmail: accountEmail,
			folderLike:   folderLike,
			syncFirst:    true,
			prune:        fullRescan, // prune only makes sense on full rescan
			fullRescan:   fullRescan,
			maxMessages:  0,
			tailCount:    0,
		}); err != nil {
			return fmt.Errorf("refresh: %w", err)
		}
	}

	hits, err := store.Search(ctx, queryOptions{
		query:      query,
		account:    accountEmail,
		folderLike: folderLike,
		since:      since,
		till:       till,
		limit:      limit,
		profile:    profile.Name,
	})
	if err != nil {
		log.Printf("warn: search store failed, scanning mailbox files directly: %v", err)
		return a.searchDirect(profile, query, folderLike, accountEmail, limit, raw, since, till, fuzzy)
	}
	if len(hits) == 0 {
		if !since.IsZero() || !till.IsZero() {
			fallbackHits, fallbackErr := store.Search(ctx, queryOptions{
				query:      query,
				account:    accountEmail,
				folderLike: folderLike,
				limit:      3,
				profile:    profile.Name,
			})
			if fallbackErr == nil && len(fallbackHits) > 0 {
				fmt.Println("No matches in the requested date range.")
				fmt.Printf("Found %d matching message(s) outside the date filter. Newest match: %s | %s | %s\n", len(fallbackHits), fallbackHits[0].Date, fallbackHits[0].From, fallbackHits[0].Subject)
				fmt.Println("Tip: rerun without --since/--till or widen the date range.")
				return nil
			}
		}
		fmt.Println("No matches.")
		return nil
	}
	return printHits(hits, limit, raw)
}

func (a *App) searchDirect(profile Profile, query, folderLike, accountEmail string, limit int, raw bool, since, till time.Time, fuzzy bool) error {
	boxes, dirToAccount, err := a.searchBoxes(profile, folderLike, accountEmail)
	if err != nil {
		return err
	}
	hits, err := directSearchMailboxes(profile.Name, boxes, dirToAccount, query, limit, since, till, fuzzy)
	if err != nil {
		return err
	}
	if len(hits) == 0 {
		if !since.IsZero() || !till.IsZero() {
			fallbackHits, fallbackErr := directSearchMailboxes(profile.Name, boxes, dirToAccount, query, 3, time.Time{}, time.Time{}, fuzzy)
			if fallbackErr == nil && len(fallbackHits) > 0 {
				fmt.Println("No matches in the requested date range.")
				fmt.Printf("Found %d matching message(s) outside the date filter. Newest match: %s | %s | %s\n", len(fallbackHits), fallbackHits[0].Date, fallbackHits[0].From, fallbackHits[0].Subject)
				fmt.Println("Tip: rerun without --since/--till or widen the date range.")
				return nil
			}
		}
		fmt.Println("No matches.")
		return nil
	}
	return printHits(hits, limit, raw)
}

func (a *App) searchBoxes(profile Profile, folderLike, accountEmail string) ([]Mailbox, map[string]string, error) {
	boxes, err := a.listMailboxes(profile)
	if err != nil {
		return nil, nil, err
	}
	dirToAccount, err := a.accountDirIndex(profile)
	if err != nil {
		return nil, nil, fmt.Errorf("account index: %w", err)
	}
	boxes, err = filterMailboxes(boxes, folderLike, accountEmail, dirToAccount)
	if err != nil {
		return nil, nil, err
	}
	return boxes, dirToAccount, nil
}

func (a *App) ingestProfile(ctx context.Context, store messageStore, profile Profile, opts ingestOptions) error {
	accountEmail := strings.ToLower(strings.TrimSpace(opts.accountEmail))
	if opts.syncFirst {
		// --sync was asked for explicitly. Continuing on failure would answer
		// from a stale cache while looking like fresh mail, which already
		// produced a false "no such email" conclusion.
		if err := a.syncProfile(profile); err != nil {
			return fmt.Errorf("sync profile %s: %w (re-run without --sync to search the existing cache)", profile.Name, err)
		}
	}

	boxes, err := a.listMailboxes(profile)
	if err != nil {
		return err
	}

	fullRescan := opts.fullRescan
	if opts.prune && !fullRescan {
		fullRescan = true
		log.Printf("info: enabling full rescan because --prune was requested")
	}

	fpCache := map[string]string{}
	if !fullRescan {
		if m, err := store.GetMetaPrefix(ctx, fmt.Sprintf("fp|%s|", profile.Name)); err == nil {
			fpCache = m
		}
	}

	dirToAccount := map[string]string{}
	if accountEmail != "" {
		idx, err := a.loadAccountDirIndex(profile)
		if err != nil {
			return fmt.Errorf("account index: %w", err)
		}
		accountDirs := idx[accountEmail]
		if len(accountDirs) == 0 {
			return fmt.Errorf("account %s not found in prefs.js", accountEmail)
		}
		for _, d := range accountDirs {
			dirToAccount[d] = accountEmail
		}
		var scoped []Mailbox
		for _, b := range boxes {
			for _, d := range accountDirs {
				if strings.HasPrefix(b.Path, d) {
					scoped = append(scoped, b)
					break
				}
			}
		}
		boxes = scoped
		if len(boxes) == 0 {
			return fmt.Errorf("no folders for account %s", accountEmail)
		}
	} else {
		// Build directory->account map for tagging.
		if idx, err := a.loadAccountDirIndex(profile); err == nil {
			for acct, dirs := range idx {
				for _, d := range dirs {
					dirToAccount[d] = acct
				}
			}
		}
	}

	if opts.folderLike != "" {
		needle := strings.ToLower(opts.folderLike)
		var filtered []Mailbox
		for _, b := range boxes {
			if strings.Contains(strings.ToLower(b.Name), needle) || strings.Contains(strings.ToLower(filepath.Base(b.Name)), needle) {
				filtered = append(filtered, b)
			}
		}
		if len(filtered) == 0 {
			return fmt.Errorf("no folders match %q", opts.folderLike)
		}
		boxes = filtered
	}

	var keepIDs []string
	var skips skipTracker
	var scannedBytes, totalBytes int64
	for _, b := range boxes {
		fi, err := os.Stat(b.Path)
		if err != nil {
			log.Printf("warn: stat %s: %v", b.Name, err)
			continue
		}
		totalBytes += fi.Size()
		fp := folderFingerprint(fi.ModTime(), fi.Size())
		fpKey := fingerprintKey(profile.Name, b.Path)
		prevFingerprint := fpCache[fpKey]
		if !fullRescan && prevFingerprint == fp {
			// Unchanged folder; skip ingest.
			continue
		}

		targetAccount := accountEmail
		if targetAccount == "" {
			targetAccount = accountForPath(b.Path, dirToAccount)
		}

		// Resume from the previous end when the folder only grew. A 118MB
		// INBOX that gained 23KB of new mail costs 23KB of parsing, not 118MB.
		// Disabled for --full/--prune, which need every message to rebuild
		// keepIDs.
		var msgs []MailSummary
		offset, incremental := int64(0), false
		if !fullRescan {
			if prevSize, known := fingerprintIngestedSize(prevFingerprint); known {
				offset, incremental = mboxAppendOffset(b.Path, prevSize, fi.Size())
			}
		}
		if incremental {
			msgs, err = scanMailboxFrom(b, offset, targetAccount, opts.maxMessages, opts.tailCount)
			if err != nil {
				// A bad offset must never mean silently missing mail.
				log.Printf("warn: incremental ingest of %s failed (%v); falling back to a full scan", b.Name, err)
				incremental = false
			} else {
				scannedBytes += fi.Size() - offset
			}
		}
		if !incremental {
			msgs, err = searchMailbox(b, func(string) bool { return true }, 0, time.Time{}, time.Time{}, opts.maxMessages, targetAccount, opts.tailCount)
			scannedBytes += fi.Size()
		}
		if err != nil {
			skips.note("ingest", b.Name, err)
			continue
		}
		decorateMessages(msgs, profile.Name, targetAccount)
		if err := store.Upsert(ctx, msgs); err != nil {
			return err
		}
		for _, m := range msgs {
			if m.MessageID != "" {
				keepIDs = append(keepIDs, m.MessageID)
			}
		}
		if err := store.SetMeta(ctx, fpKey, fp); err != nil {
			log.Printf("warn: save fingerprint %s: %v", b.Name, err)
		}
	}
	skips.report()
	if scannedBytes > 0 && totalBytes > scannedBytes {
		log.Printf("info: ingest parsed %s of %s (resumed where the previous ingest stopped)", byteSize(scannedBytes), byteSize(totalBytes))
	}
	if opts.prune && fullRescan {
		if err := store.PruneMissing(ctx, profile.Name, keepIDs); err != nil {
			return err
		}
	}
	_ = store.SetMeta(ctx, fmt.Sprintf("last_scan.%s", profile.Name), time.Now().UTC().Format(time.RFC3339))
	if fullRescan {
		_ = store.SetMeta(ctx, fmt.Sprintf("last_full_scan.%s", profile.Name), time.Now().UTC().Format(time.RFC3339))
	}
	return nil
}

func (a *App) syncProfile(profile Profile) error {
	return a.syncProfileWithOptions(profile, syncOptions{Timeout: syncTimeout()})
}

func (a *App) syncProfileWithOptions(profile Profile, opts syncOptions) error {
	if opts.Timeout <= 0 {
		opts.Timeout = syncTimeout()
	}
	baseCmd := findMailCommand()
	args := append(profileSelectArgs(profile), "-mail")
	env := os.Environ()
	if opts.Headless {
		args = append([]string{"-headless"}, args...)
	} else if !guiSessionAvailable() {
		// Order matters. Joining the session of an already-running mail client
		// is the only option that works for a Flatpak build, and it is what a
		// plain ssh shell needs; Xvfb is the fallback for when nothing is
		// running. Failing outright beats the old silent degrade to a
		// stale-cache read that made --sync look like it had worked.
		if session, ok := detectRunningMailSession(); ok {
			log.Printf("info: no display in this shell; joining the %s (%s)", session.Source, session.display())
			env = session.apply(env)
		} else if display, _, cleanup, err := startVirtualDisplay(); err == nil {
			defer cleanup()
			env = append(env, "DISPLAY="+display)
		} else if mailCommandUsesFlatpak(baseCmd) {
			return fmt.Errorf("cannot sync: no GUI session in this shell, no running Betterbird/Thunderbird to join, and no virtual display (%w). "+
				"Start the mail client on the desktop session, set THUNDERBIRD_BIN to a native binary, or install Xvfb", err)
		} else {
			log.Printf("info: no display available (%v); using -headless", err)
			args = append([]string{"-headless"}, args...)
		}
	}
	// Snapshot the mail tree so a timeout can be judged on evidence instead of
	// assumed to have worked.
	before := mailTreeFingerprint(profile)

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, baseCmd[0], append(baseCmd[1:], args...)...)
	cmd.Env = env
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	runErr := cmd.Run()
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	if runErr != nil && !timedOut {
		return runErr
	}

	// A sync that touched nothing is the failure this whole release is about:
	// it used to print "assuming mail client had enough time to fetch" and
	// return success, so a profile that never opened (stale lock file, profile
	// chooser waiting on an invisible display) was indistinguishable from a
	// mailbox with no new mail.
	after := mailTreeFingerprint(profile)
	if before != after {
		return nil
	}
	if timedOut {
		return fmt.Errorf("sync ran for %s but did not modify the mail store — the client most likely never opened the profile. "+
			"Check for a stale %s/lock, and run `%s -profile %s -mail` on a real display to see any dialog it is waiting on",
			opts.Timeout, profile.AbsolutePath, baseCmd[0], profile.AbsolutePath)
	}
	log.Printf("info: sync completed without changing the mail store (no new mail, or nothing to fetch)")
	return nil
}

// profileSelectArgs selects a profile by absolute path when one is known.
//
// `-P <name>` silently falls back to the graphical profile manager when the
// name does not bind — which, on a headless host, means the client sits on an
// invisible "Choose User Profile" dialog forever while tb reports success.
// `-profile <path>` always binds.
func profileSelectArgs(profile Profile) []string {
	if strings.TrimSpace(profile.AbsolutePath) != "" {
		return []string{"-profile", profile.AbsolutePath}
	}
	return []string{"-P", profile.Name}
}

// mailTreeFingerprint summarises the size and mtime of a profile's mail stores.
// It is deliberately cheap: names, sizes and mtimes only, never contents.
func mailTreeFingerprint(profile Profile) string {
	if strings.TrimSpace(profile.AbsolutePath) == "" {
		return ""
	}
	h := sha256.New()
	for _, sub := range []string{"Mail", "ImapMail"} {
		root := filepath.Join(profile.AbsolutePath, sub)
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			fmt.Fprintf(h, "%s|%d|%d\n", path, info.Size(), info.ModTime().UnixNano())
			return nil
		})
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (a *App) sync(profileName string, opts syncOptions) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	return a.syncProfileWithOptions(profile, opts)
}

func (a *App) compose(profileName string, req composeRequest, openComposer, sendNow bool, verifyWait time.Duration) error {
	baseCmd := findMailCommand()
	var (
		profile Profile
		err     error
	)
	if profileName != "" || sendNow {
		profile, err = a.resolveProfile(profileName)
		if err != nil {
			return err
		}
	}
	composeArg, err := buildComposeArg(profile, req)
	if err != nil {
		return err
	}
	if !openComposer && sendNow {
		res, err := a.sendHeadlessly(profile, req)
		if err == nil {
			reportSend(res)
			return a.verifySend(profile, req, res, verifyWait)
		}
		if !errors.Is(err, errDirectSendUnsupported) {
			// The send may still have gone out; sendHeadlessly reports the
			// Message-ID in the error text when it did.
			return err
		}
		// The isolated-profile fallback drives the GUI composer, which has no
		// way to set In-Reply-To/References. Silently dropping them would send
		// an unthreaded reply that looks fine locally and opens a new ticket
		// on the receiving end, so refuse instead.
		if req.threaded() {
			return fmt.Errorf("direct send is unavailable for %q and the isolated-profile fallback cannot set In-Reply-To/References; "+
				"send without threading headers or use an identity that supports direct send (see tb features)", req.From)
		}
		if err := runIsolatedHeadlessSend(baseCmd, profile, composeArg); err != nil {
			return err
		}
		// No Message-ID is knowable here: the GUI composer minted it.
		log.Printf("sent: via isolated profile clone (Message-ID unknown to tb); verify with: tb mail sentcheck --from %s --subject %q", req.From, req.Subject)
		return nil
	}
	args := []string{"-compose", composeArg}
	if sendNow {
		return fmt.Errorf("Thunderbird/Betterbird does not provide a supported CLI -send path; use --send --open=false for automated send")
	}
	if profileName != "" {
		args = append(profileSelectArgs(profile), args...)
	}
	cmd := exec.Command(baseCmd[0], append(baseCmd[1:], args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if openComposer {
		return cmd.Run()
	}
	return nil
}

// reportSend prints the proof that a send happened. Headless send used to
// print nothing at all on success, so a caller checking a stale local Sent
// folder could conclude "silent no-op" and re-send a message that had already
// gone out.
func reportSend(res sendResult) {
	log.Printf("sent: %s -> %s", res.From, strings.Join(res.Recipients, ", "))
	log.Printf("  message-id: %s", res.MessageID)
	log.Printf("  transport:  %s via %s", res.Transport, res.Server)
	if res.SentCopy != "" {
		log.Printf("  sent copy:  %s", res.SentCopy)
	}
}

// verifySend confirms the message from the server side rather than from the
// local mbox cache, which lags and has already caused a duplicate send.
func (a *App) verifySend(profile Profile, req composeRequest, res sendResult, wait time.Duration) error {
	if wait <= 0 {
		return nil
	}
	account, err := resolveSendAccount(profile, req.From)
	if err != nil {
		return fmt.Errorf("message sent (Message-ID %s) but verification could not resolve the account: %w", res.MessageID, err)
	}
	check, err := pollSentMessages(account, "", res.MessageID, "", wait, 1)
	if err != nil {
		return fmt.Errorf("message sent (Message-ID %s) but not confirmed in the Sent mailbox within %s: %w; "+
			"do NOT re-send — re-check with: tb mail sentcheck --from %s --message-id %s",
			res.MessageID, wait, err, req.From, res.MessageID)
	}
	log.Printf("  verified:   Message-ID present in %s", check.Mailbox)
	return nil
}

// readBodyFile loads a message body from disk, or from stdin when path is "-".
// Long bodies are quoting hell to pass as --body over ssh.
func readBodyFile(path string) (string, error) {
	if path == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read body from stdin: %w", err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read --body-file: %w", err)
	}
	return string(data), nil
}

func (a *App) loadProfiles() ([]Profile, error) {
	path := filepath.Join(a.Root, "profiles.ini")
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var profiles []Profile
	var current map[string]string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section := strings.Trim(line, "[]")
			if current != nil {
				profiles = append(profiles, mapToProfile(a.Root, current))
			}
			if strings.HasPrefix(strings.ToLower(section), "profile") {
				current = map[string]string{}
			} else {
				current = nil
			}
			continue
		}
		if current == nil {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		current[parts[0]] = parts[1]
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if current != nil {
		profiles = append(profiles, mapToProfile(a.Root, current))
	}
	return profiles, nil
}

// fetch ingests Thunderbird mailboxes into the configured cache backend (optionally syncing first).
func (a *App) fetch(profileName, folderLike, accountEmail string, syncFirst, prune, fullRescan bool, maxMessages, tailCount int) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	if syncFirst {
		// --sync was asked for explicitly. Continuing on failure would answer
		// from a stale cache while looking like fresh mail, which already
		// produced a false "no such email" conclusion.
		if err := a.syncProfile(profile); err != nil {
			return fmt.Errorf("sync profile %s: %w (re-run without --sync to search the existing cache)", profile.Name, err)
		}
	}
	store, err := openStore()
	if err != nil {
		return fmt.Errorf("open fetch store: %w", err)
	}
	defer store.Close()

	ctx := context.Background()
	return a.ingestProfile(ctx, store, profile, ingestOptions{
		accountEmail: strings.ToLower(strings.TrimSpace(accountEmail)),
		folderLike:   folderLike,
		syncFirst:    false,
		prune:        prune,
		fullRescan:   fullRescan,
		maxMessages:  maxMessages,
		tailCount:    tailCount,
	})
}

func mapToProfile(root string, kv map[string]string) Profile {
	p := Profile{
		Name:       kv["Name"],
		Path:       kv["Path"],
		IsRelative: kv["IsRelative"] == "1",
		Default:    kv["Default"] == "1",
	}
	if p.Name == "" {
		p.Name = filepath.Base(p.Path)
	}
	if p.IsRelative {
		p.AbsolutePath = filepath.Join(root, filepath.FromSlash(p.Path))
	} else {
		p.AbsolutePath = filepath.Clean(p.Path)
	}
	return p
}

func (a *App) resolveProfile(name string) (Profile, error) {
	profiles, err := a.loadProfiles()
	if err != nil {
		return Profile{}, fmt.Errorf("load profiles: %w", err)
	}
	if name == "" {
		// Honour the profiles.ini default only if it actually holds mail: a
		// fresh/empty "default" profile otherwise makes every search report
		// "No matches" while the real mail sits in another profile.
		var richest Profile
		var bestBytes int64
		for _, p := range profiles {
			if b := profileMailBytes(p); b > bestBytes {
				bestBytes, richest = b, p
			}
		}
		for _, p := range profiles {
			if p.Default {
				if bestBytes == 0 || profileMailBytes(p) > 0 {
					return p, nil
				}
				log.Printf("info: profile %q has no mail; using %q instead", p.Name, richest.Name)
				return richest, nil
			}
		}
		if bestBytes > 0 {
			return richest, nil
		}
		if len(profiles) > 0 {
			return profiles[0], nil
		}
		return Profile{}, fmt.Errorf("no profiles found in %s", filepath.Join(a.Root, "profiles.ini"))
	}
	needle := strings.ToLower(name)
	for _, p := range profiles {
		if strings.ToLower(p.Name) == needle || strings.ToLower(filepath.Base(p.Path)) == needle || strings.ToLower(filepath.Base(p.AbsolutePath)) == needle {
			return p, nil
		}
	}
	if filepath.IsAbs(name) {
		return Profile{Name: filepath.Base(name), Path: name, AbsolutePath: name}, nil
	}
	alt := filepath.Join(a.Root, name)
	if _, err := os.Stat(alt); err == nil {
		return Profile{Name: filepath.Base(name), Path: name, AbsolutePath: alt}, nil
	}
	return Profile{}, fmt.Errorf("profile %s not found", name)
}

func (a *App) loadAccountDirIndex(p Profile) (map[string][]string, error) {
	prefsPath := filepath.Join(p.AbsolutePath, "prefs.js")
	prefs, err := parsePrefs(prefsPath)
	if err != nil {
		return nil, err
	}
	acctsStr := prefs["mail.accountmanager.accounts"]
	accts := splitCSV(acctsStr)
	emailDirs := map[string][]string{}
	for _, acc := range accts {
		server := prefs[fmt.Sprintf("mail.account.%s.server", acc)]
		idents := splitCSV(prefs[fmt.Sprintf("mail.account.%s.identities", acc)])
		dir := prefs[fmt.Sprintf("mail.server.%s.directory", server)]
		dirRel := prefs[fmt.Sprintf("mail.server.%s.directory-rel", server)]
		if relPath := resolveProfileRelativeDir(p.AbsolutePath, dirRel); relPath != "" {
			if dir == "" || !pathUsableForProfile(dir, p.AbsolutePath) {
				dir = relPath
			}
		}
		if dir == "" {
			continue
		}
		for _, id := range idents {
			email := prefs[fmt.Sprintf("mail.identity.%s.useremail", id)]
			if email == "" {
				continue
			}
			email = strings.ToLower(email)
			emailDirs[email] = append(emailDirs[email], dir)
		}
	}
	for k, dirs := range emailDirs {
		emailDirs[k] = uniqueStrings(dirs)
	}
	return emailDirs, nil
}

func resolveProfileRelativeDir(profilePath, dirRel string) string {
	if strings.HasPrefix(dirRel, "[ProfD]") {
		return filepath.Join(profilePath, filepath.FromSlash(strings.TrimPrefix(dirRel, "[ProfD]")))
	}
	if dirRel != "" {
		return filepath.Clean(dirRel)
	}
	return ""
}

func pathUsableForProfile(path, profilePath string) bool {
	path = filepath.Clean(path)
	profilePath = filepath.Clean(profilePath)
	if strings.HasPrefix(path, profilePath+string(os.PathSeparator)) {
		return true
	}
	_, err := os.Stat(path)
	return err == nil
}

func parsePrefs(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`^user_pref\("([^"]+)",\s*(.+)\);$`)
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		matches := re.FindStringSubmatch(line)
		if len(matches) == 3 {
			value := strings.TrimSpace(matches[2])
			if strings.HasPrefix(value, "\"") {
				unquoted, err := strconv.Unquote(value)
				if err == nil {
					m[matches[1]] = unquoted
					continue
				}
				m[matches[1]] = strings.Trim(value, "\"")
				continue
			}
			m[matches[1]] = value
		}
	}
	return m, nil
}

func (a *App) listMailboxes(p Profile) ([]Mailbox, error) {
	roots := []string{
		filepath.Join(p.AbsolutePath, "Mail"),
		filepath.Join(p.AbsolutePath, "ImapMail"),
	}
	var boxes []Mailbox
	for _, root := range roots {
		info, err := os.Stat(root)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if !info.IsDir() {
			continue
		}
		filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return filepath.SkipDir
			}
			if d.IsDir() {
				if strings.HasSuffix(d.Name(), ".mozmsgs") {
					return filepath.SkipDir
				}
				return nil
			}
			base := d.Name()
			ext := filepath.Ext(base)
			if ext != "" && ext != ".mbox" {
				return nil
			}
			if strings.HasSuffix(base, ".msf") || strings.HasSuffix(base, ".dat") || strings.HasSuffix(base, ".json") || strings.HasSuffix(base, ".db") || strings.HasSuffix(base, ".sqlite") {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			rel, err := filepath.Rel(p.AbsolutePath, path)
			if err != nil {
				rel = path
			}
			boxes = append(boxes, Mailbox{
				Name: rel,
				Path: path,
				Size: info.Size(),
			})
			return nil
		})
	}
	sort.Slice(boxes, func(i, j int) bool { return boxes[i].Name < boxes[j].Name })
	return boxes, nil
}

func findMailbox(boxes []Mailbox, name string) (Mailbox, bool) {
	needle := strings.ToLower(name)
	var fallback Mailbox
	for _, b := range boxes {
		base := strings.ToLower(filepath.Base(b.Name))
		rel := strings.ToLower(b.Name)
		if base == needle || rel == needle {
			return b, true
		}
		if strings.Contains(rel, needle) || strings.Contains(base, needle) {
			if fallback.Path == "" {
				fallback = b
			}
		}
	}
	if fallback.Path != "" {
		return fallback, true
	}
	return Mailbox{}, false
}

func findMailCommand() []string {
	if env := strings.TrimSpace(os.Getenv("THUNDERBIRD_BIN")); env != "" {
		if _, err := os.Stat(env); err == nil {
			return []string{env}
		}
	}
	if path, err := exec.LookPath("betterbird"); err == nil {
		return []string{path}
	}
	if path, err := exec.LookPath("thunderbird"); err == nil {
		return []string{path}
	}
	if flatpak, err := exec.LookPath("flatpak"); err == nil {
		id := strings.TrimSpace(os.Getenv("THUNDERBIRD_FLATPAK_ID"))
		if id == "" {
			id = "eu.betterbird.Betterbird"
		}
		return []string{flatpak, "run", id}
	}
	// Fallback; caller will fail if not present.
	return []string{"thunderbird"}
}

func syncTimeout() time.Duration {
	raw := strings.TrimSpace(os.Getenv("TB_SYNC_TIMEOUT"))
	if raw == "" {
		return 90 * time.Second
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 90 * time.Second
	}
	return d
}

func validateSyncEnvironment(baseCmd []string) error {
	if !mailCommandUsesFlatpak(baseCmd) {
		return nil
	}
	if guiSessionAvailable() {
		return nil
	}
	return fmt.Errorf("mail sync via Flatpak Betterbird/Thunderbird requires a real GUI session; set THUNDERBIRD_BIN to a native binary or run without --sync")
}

func mailCommandUsesFlatpak(baseCmd []string) bool {
	if len(baseCmd) == 0 {
		return false
	}
	return filepath.Base(baseCmd[0]) == "flatpak"
}

func guiSessionAvailable() bool {
	return strings.TrimSpace(os.Getenv("DISPLAY")) != "" ||
		strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != "" ||
		strings.TrimSpace(os.Getenv("MIR_SOCKET")) != ""
}

func syncCommandArgs(baseCmd []string, profile Profile) []string {
	args := append(profileSelectArgs(profile), "-mail")
	if !mailCommandUsesFlatpak(baseCmd) {
		return append([]string{"-headless"}, args...)
	}
	if guiSessionAvailable() {
		return args
	}
	return append([]string{"-headless"}, args...)
}

func runIsolatedHeadlessSend(baseCmd []string, profile Profile, composeArg string) error {
	clonedProfile, cleanup, err := cloneProfileForSend(profile)
	if err != nil {
		return err
	}

	if err := runAutomatedComposeSend(baseCmd, clonedProfile, composeArg); err != nil {
		return fmt.Errorf("headless send via isolated profile %s: %w", clonedProfile, err)
	}
	cleanup()
	return nil
}

func buildComposeArg(profile Profile, req composeRequest) (string, error) {
	var parts []string
	if profile.AbsolutePath != "" && req.From != "" {
		preselectID, err := composeIdentityID(profile, req.From)
		if err != nil {
			return "", err
		}
		if preselectID != "" {
			parts = append(parts, fmt.Sprintf("preselectid=%s", preselectID))
		} else {
			parts = append(parts, fmt.Sprintf("from=%s", req.From))
		}
	} else if req.From != "" {
		parts = append(parts, fmt.Sprintf("from=%s", req.From))
	}
	parts = append(parts, fmt.Sprintf("to=%s", req.To))
	if req.Cc != "" {
		parts = append(parts, fmt.Sprintf("cc=%s", req.Cc))
	}
	if req.Subject != "" {
		parts = append(parts, fmt.Sprintf("subject=%s", req.Subject))
	}
	if req.Body != "" {
		parts = append(parts, fmt.Sprintf("body=%s", req.Body))
	}
	return strings.Join(parts, ","), nil
}

func composeIdentityID(profile Profile, email string) (string, error) {
	prefsPath := filepath.Join(profile.AbsolutePath, "prefs.js")
	prefs, err := parsePrefs(prefsPath)
	if err != nil {
		return "", err
	}
	email = strings.ToLower(strings.TrimSpace(email))
	for key, value := range prefs {
		if !strings.HasPrefix(key, "mail.identity.") || !strings.HasSuffix(key, ".useremail") {
			continue
		}
		if strings.ToLower(strings.TrimSpace(value)) != email {
			continue
		}
		return strings.TrimSuffix(strings.TrimPrefix(key, "mail.identity."), ".useremail"), nil
	}
	return "", nil
}

func runAutomatedComposeSend(baseCmd []string, clonedProfile, composeArg string) error {
	display, xvfb, cleanup, err := startVirtualDisplay()
	if err != nil {
		return err
	}
	defer cleanup()

	args := []string{
		"--new-instance",
		"--no-remote",
		"--profile", clonedProfile,
		"-compose", composeArg,
	}
	cmd := exec.Command(baseCmd[0], append(baseCmd[1:], args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	if err := cmd.Start(); err != nil {
		_ = xvfb.Process.Kill()
		_, _ = xvfb.Process.Wait()
		return err
	}
	defer func() {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	}()

	wid, err := waitForComposeWindow(display, 45*time.Second)
	if err != nil {
		return err
	}
	time.Sleep(2 * time.Second)
	if err := tryComposeSend(display, wid); err != nil {
		return err
	}
	closed, err := waitForComposeWindowGone(display, wid, 20*time.Second)
	if err != nil {
		return err
	}
	if !closed {
		return fmt.Errorf("compose window %s remained open after automated send attempt", wid)
	}
	time.Sleep(5 * time.Second)
	return nil
}

// startVirtualDisplay brings up an Xvfb server and returns its display name.
//
// It asks Xvfb to pick the display number itself via -displayfd rather than
// hardcoding :98 and waiting for /tmp/.X11-unix/X98 to appear. A leftover
// socket from a crashed Xvfb satisfies that check instantly, so tb used to hand
// the mail client a display number with no server behind it — the client then
// died with "Exiting due to channel error" while tb reported success.
func startVirtualDisplay() (string, *exec.Cmd, func(), error) {
	if _, err := exec.LookPath("Xvfb"); err != nil {
		return "", nil, nil, fmt.Errorf("Xvfb is required for unattended compose/send: %w", err)
	}
	readFD, writeFD, err := os.Pipe()
	if err != nil {
		return "", nil, nil, err
	}
	defer readFD.Close()

	// ExtraFiles[0] becomes fd 3 in the child.
	cmd := exec.Command("Xvfb", "-displayfd", "3", "-screen", "0", "1280x900x24")
	cmd.ExtraFiles = []*os.File{writeFD}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		writeFD.Close()
		return "", nil, nil, err
	}
	writeFD.Close() // the child holds the only writer now

	cleanup := func() {
		if cmd.Process == nil {
			return
		}
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			_ = cmd.Process.Kill()
		}
		_, _ = cmd.Process.Wait()
	}

	type result struct {
		display string
		err     error
	}
	done := make(chan result, 1)
	go func() {
		buf := make([]byte, 32)
		n, err := readFD.Read(buf)
		if err != nil {
			done <- result{err: err}
			return
		}
		num := strings.TrimSpace(string(buf[:n]))
		if num == "" {
			done <- result{err: fmt.Errorf("Xvfb reported an empty display number")}
			return
		}
		done <- result{display: ":" + num}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			cleanup()
			return "", nil, nil, fmt.Errorf("Xvfb failed to report a display: %w", res.err)
		}
		return res.display, cmd, cleanup, nil
	case <-time.After(10 * time.Second):
		cleanup()
		return "", nil, nil, fmt.Errorf("timed out waiting for Xvfb to report a display number")
	}
}

func waitForComposeWindow(display string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		wid, err := xdotoolWindowSearch(display, "Write:")
		if err == nil && wid != "" {
			return wid, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return "", fmt.Errorf("timed out waiting for compose window on %s", display)
}

func waitForComposeWindowGone(display, wid string, timeout time.Duration) (bool, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		found, err := xdotoolWindowExists(display, wid)
		if err != nil {
			return false, err
		}
		if !found {
			return true, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false, nil
}

func tryComposeSend(display, wid string) error {
	if _, err := exec.LookPath("xdotool"); err != nil {
		return fmt.Errorf("xdotool is required for unattended compose/send: %w", err)
	}
	for _, args := range [][]string{
		{"key", "--window", wid, "ctrl+Return"},
		{"key", "--window", wid, "alt+s"},
		{"mousemove", "--window", wid, "40", "90", "click", "1"},
	} {
		cmd := exec.Command("xdotool", args...)
		cmd.Env = append(os.Environ(), "DISPLAY="+display)
		_ = cmd.Run()
		time.Sleep(2 * time.Second)
		found, err := xdotoolWindowExists(display, wid)
		if err != nil {
			return err
		}
		if !found {
			return nil
		}
	}
	return nil
}

func xdotoolWindowSearch(display, name string) (string, error) {
	cmd := exec.Command("xdotool", "search", "--name", name)
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	lines := strings.Fields(strings.TrimSpace(string(out)))
	if len(lines) == 0 {
		return "", fmt.Errorf("no windows found for %q", name)
	}
	return lines[len(lines)-1], nil
}

func xdotoolWindowExists(display, wid string) (bool, error) {
	cmd := exec.Command("xdotool", "getwindowname", wid)
	cmd.Env = append(os.Environ(), "DISPLAY="+display)
	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func cloneProfileForSend(profile Profile) (string, func(), error) {
	parent := filepath.Join(filepath.Dir(profile.AbsolutePath), ".tb-send")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", nil, fmt.Errorf("create send-profile root: %w", err)
	}
	dst, err := os.MkdirTemp(parent, "profile-")
	if err != nil {
		return "", nil, fmt.Errorf("create send-profile tempdir: %w", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(dst)
	}
	if err := copyProfileTree(profile.AbsolutePath, dst); err != nil {
		cleanup()
		return "", nil, err
	}
	return dst, cleanup, nil
}

func copyProfileTree(srcRoot, dstRoot string) error {
	return filepath.WalkDir(srcRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcRoot, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		name := d.Name()
		if shouldSkipProfilePath(rel, name, d.IsDir()) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		target := filepath.Join(dstRoot, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			linkTarget, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(linkTarget, target)
		}
		return copyProfileFile(path, target, info.Mode().Perm())
	})
}

func shouldSkipProfilePath(rel, name string, isDir bool) bool {
	base := strings.ToLower(name)
	if rel == "." {
		return false
	}
	if isDir {
		return true
	}
	if strings.Contains(rel, string(os.PathSeparator)) {
		return true
	}
	switch base {
	case "prefs.js",
		"user.js",
		"logins.json",
		"logins-backup.json",
		"key4.db",
		"cert9.db",
		"pkcs11.txt",
		"openpgp.sqlite",
		"permissions.sqlite",
		"compatibility.ini",
		"xulstore.json",
		"addonstartup.json.lz4",
		"addons.json",
		"extensions.json",
		"extension-preferences.json",
		"extension-settings.json",
		"handlers.json":
		return false
	}
	return true
}

func copyProfileFile(src, dst string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func readMailboxRecent(box Mailbox, limit int, query string) ([]MailSummary, error) {
	f, err := os.Open(box.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	reader := mbox.NewReader(f)
	query = strings.ToLower(query)
	var buf []MailSummary
	for {
		msgReader, err := reader.NextMessage()
		if err == io.EOF {
			break
		}
		if err != nil {
			return buf, err
		}
		summary, searchText, err := parseMessage(msgReader, box.Name)
		if err != nil {
			continue
		}
		if query != "" && !strings.Contains(searchText, query) {
			continue
		}
		buf = append(buf, summary)
		if query == "" && limit > 0 && len(buf) > limit {
			buf = buf[1:]
		}
		if query != "" && limit > 0 && len(buf) >= limit {
			break
		}
	}
	return buf, nil
}

type matcherFunc func(string) bool

func makeMatcher(q string, fuzzy bool) matcherFunc {
	if !fuzzy {
		q = strings.ToLower(q)
		return func(s string) bool {
			return strings.Contains(s, q)
		}
	}
	tokens := strings.Fields(strings.ToLower(q))
	return func(s string) bool {
		for _, t := range tokens {
			if !strings.Contains(s, t) {
				return false
			}
		}
		return true
	}
}

func makeSearchMatcher(q string, fuzzy bool) matcherFunc {
	tokens := strings.Fields(strings.ToLower(strings.TrimSpace(q)))
	if len(tokens) == 0 {
		return func(string) bool { return true }
	}
	return func(s string) bool {
		s = strings.ToLower(s)
		for _, token := range tokens {
			if !strings.Contains(s, token) {
				return false
			}
		}
		return true
	}
}

func decorateMessages(msgs []MailSummary, profile string, account string) {
	for i := range msgs {
		msgs[i].Profile = profile
		if msgs[i].Account == "" && account != "" {
			msgs[i].Account = account
		}
		msgs[i].MessageID = cleanUTF8(msgs[i].MessageID)
		msgs[i].Folder = cleanUTF8(msgs[i].Folder)
		msgs[i].Date = cleanUTF8(msgs[i].Date)
		msgs[i].Subject = cleanUTF8(msgs[i].Subject)
		msgs[i].From = cleanUTF8(msgs[i].From)
		msgs[i].Snippet = cleanUTF8(msgs[i].Snippet)
		msgs[i].Search = cleanUTF8(msgs[i].Search)
		if msgs[i].Search == "" {
			msgs[i].Search = strings.ToLower(strings.Join([]string{msgs[i].Subject, msgs[i].From, msgs[i].Snippet}, " "))
		}
		msgs[i].Search = cleanUTF8(msgs[i].Search)
	}
}

func cleanUTF8(s string) string {
	if s == "" || utf8.ValidString(s) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if r == utf8.RuneError {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func printHits(hits []MailSummary, limit int, raw bool) error {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].When.IsZero() && hits[j].When.IsZero() {
			return hits[i].Date > hits[j].Date
		}
		if hits[i].When.IsZero() {
			return false
		}
		if hits[j].When.IsZero() {
			return true
		}
		return hits[i].When.After(hits[j].When)
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}

	if raw {
		for _, h := range hits {
			date := h.Date
			if !h.When.IsZero() {
				date = h.When.Format("2006-01-02 15:04")
			}
			fmt.Printf("%s | %s | %s | %s | %s\n",
				date,
				truncate(h.Folder, 22),
				truncate(h.From, 40),
				truncate(h.Subject, 60),
				truncate(h.Snippet, 120))
		}
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "DATE\tFOLDER\tFROM\tSUBJECT\tSNIPPET\n")
	fmt.Fprintf(w, "----\t------\t----\t-------\t-------\n")
	for _, h := range hits {
		date := h.Date
		if !h.When.IsZero() {
			date = h.When.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			date,
			truncate(h.Folder, 24),
			truncate(h.From, 40),
			truncate(h.Subject, 60),
			truncate(h.Snippet, 120))
	}
	w.Flush()
	return nil
}

func directSearchMailboxes(profile string, boxes []Mailbox, dirToAccount map[string]string, query string, limit int, since, till time.Time, fuzzy bool) ([]MailSummary, error) {
	candidates, err := shortlistMailboxes(query, boxes)
	if err != nil {
		log.Printf("warn: direct search shortlist failed, scanning all candidate mailboxes: %v", err)
		candidates = boxes
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	match := makeSearchMatcher(query, fuzzy)
	var hits []MailSummary
	for _, box := range candidates {
		account := accountForPath(box.Path, dirToAccount)
		boxHits, err := searchMailbox(box, match, limit, since, till, 0, account, 0)
		if err != nil {
			log.Printf("warn: search %s: %v", box.Name, err)
			continue
		}
		decorateMessages(boxHits, profile, account)
		hits = append(hits, boxHits...)
	}
	return hits, nil
}

func accountForPath(path string, dirToAccount map[string]string) string {
	for dir, acct := range dirToAccount {
		if strings.HasPrefix(path, dir) {
			return acct
		}
	}
	return ""
}

func findMailBinary() (string, error) {
	if env := strings.TrimSpace(os.Getenv("THUNDERBIRD_BIN")); env != "" {
		if _, err := os.Stat(env); err == nil {
			return env, nil
		}
	}
	candidates := []string{"betterbird", "thunderbird"}
	for _, c := range candidates {
		if path, err := exec.LookPath(c); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("neither betterbird nor thunderbird found in PATH")
}

func (a *App) buildFolderIndex(box Mailbox, accountLabel string, maxMessages int, tailCount int) (FolderIndex, error) {
	full, err := searchMailbox(box, func(string) bool { return true }, 0, time.Time{}, time.Time{}, maxMessages, accountLabel, tailCount)
	if err != nil {
		return FolderIndex{}, err
	}
	fi, err := os.Stat(box.Path)
	if err != nil {
		return FolderIndex{}, err
	}
	return FolderIndex{
		ModTime:  fi.ModTime().Unix(),
		Size:     fi.Size(),
		Messages: full,
		SavedAt:  time.Now().UTC(),
		Complete: true,
	}, nil
}

func searchMailbox(box Mailbox, match matcherFunc, limit int, since, till time.Time, maxMessages int, accountLabel string, tailCount int) ([]MailSummary, error) {
	f, err := os.Open(box.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return scanMailboxReader(f, box, match, limit, since, till, maxMessages, accountLabel, tailCount)
}

// scanMailboxFrom reads only the messages appended after offset.
//
// mbox files grow by appending, so re-reading the whole file to pick up a few
// new messages means scanning megabytes to find kilobytes. The caller is
// responsible for proving the offset is still a valid message boundary.
func scanMailboxFrom(box Mailbox, offset int64, accountLabel string, maxMessages, tailCount int) ([]MailSummary, error) {
	f, err := os.Open(box.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return scanMailboxReader(f, box, func(string) bool { return true }, 0, time.Time{}, time.Time{}, maxMessages, accountLabel, tailCount)
}

func scanMailboxReader(r io.Reader, box Mailbox, match matcherFunc, limit int, since, till time.Time, maxMessages int, accountLabel string, tailCount int) ([]MailSummary, error) {
	reader := mbox.NewReader(r)
	var hits []MailSummary
	seen := 0
	warnCount := 0
	for {
		msgReader, err := reader.NextMessage()
		if err == io.EOF {
			break
		}
		if err != nil {
			// Skip bad messages but return what we have so far.
			if warnCount < 3 {
				log.Printf("warn: %s: %v", box.Name, err)
			}
			warnCount++
			continue
		}
		seen++
		if maxMessages > 0 && seen > maxMessages && tailCount == 0 {
			break
		}
		summary, searchText, err := parseMessage(msgReader, box.Name)
		if err != nil {
			continue
		}
		if !since.IsZero() && !summary.When.IsZero() && summary.When.Before(since) {
			continue
		}
		if !till.IsZero() && !summary.When.IsZero() && summary.When.After(till) {
			continue
		}
		if accountLabel != "" {
			summary.Account = accountLabel
		}
		// Tag folder type for awareness (trash/spam).
		lower := strings.ToLower(box.Name)
		switch {
		case strings.Contains(lower, "trash") || strings.Contains(lower, "deleted"):
			summary.FolderTag = "trash"
		case strings.Contains(lower, "spam") || strings.Contains(lower, "junk"):
			summary.FolderTag = "spam"
		}
		summary.Search = searchText
		if match(searchText) {
			hits = append(hits, summary)
			if tailCount > 0 && len(hits) > tailCount {
				hits = hits[1:]
			}
			if tailCount == 0 && limit > 0 && len(hits) > limit {
				hits = hits[1:]
			}
		}
	}
	return hits, nil
}

func rawSearch(query string, boxes []Mailbox, limit int, noFancy bool) error {
	if len(boxes) == 0 {
		return fmt.Errorf("no folders match")
	}
	total := 0
	maxPerFile := limit
	if maxPerFile <= 0 {
		maxPerFile = 1000
	}
	patternArgs := buildRipgrepPattern(query)
	var linesOut []struct {
		Folder string
		Line   string
		Text   string
	}
	for _, b := range boxes {
		if limit > 0 && total >= limit {
			break
		}
		args := []string{"-n", "--no-heading", "--color", "never", "--max-count", fmt.Sprintf("%d", maxPerFile)}
		args = append(args, patternArgs...)
		args = append(args, b.Path)
		cmd := exec.Command("rg", args...)
		out, err := cmd.CombinedOutput()
		if err != nil && len(out) == 0 {
			log.Printf("warn: ripgrep %s: %v", b.Name, err)
			continue
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		for _, line := range lines {
			if line == "" {
				continue
			}
			total++
			if limit > 0 && total > limit {
				break
			}
			parts := strings.SplitN(line, ":", 2)
			if len(parts) != 2 {
				linesOut = append(linesOut, struct {
					Folder string
					Line   string
					Text   string
				}{Folder: b.Name, Line: "", Text: truncate(line, 160)})
				continue
			}
			linesOut = append(linesOut, struct {
				Folder string
				Line   string
				Text   string
			}{Folder: b.Name, Line: parts[0], Text: truncate(strings.TrimSpace(parts[1]), 160)})
		}
	}
	if total == 0 {
		fmt.Println("No matches.")
		return nil
	}

	if noFancy {
		for _, l := range linesOut {
			if l.Line != "" {
				fmt.Printf("%s:%s | %s\n", l.Folder, l.Line, l.Text)
			} else {
				fmt.Printf("%s | %s\n", l.Folder, l.Text)
			}
		}
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "Folder\tLine\tText\n")
	for _, l := range linesOut {
		fmt.Fprintf(w, "%s\t%s\t%s\n", truncate(l.Folder, 40), l.Line, l.Text)
	}
	w.Flush()
	return nil
}

func buildRipgrepPattern(q string) []string {
	// Prefer word-bounded, case-insensitive regex for simple tokens to avoid noisy base64 hits.
	if simpleWord(q) {
		pat := fmt.Sprintf("(?i)\\b%s\\b", regexp.QuoteMeta(q))
		return []string{"--pcre2", "--regexp", pat}
	}
	return []string{"--fixed-strings", "--ignore-case", q}
}

func simpleWord(s string) bool {
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return len(s) > 0
}

func shortlistMailboxes(query string, boxes []Mailbox) ([]Mailbox, error) {
	query = strings.TrimSpace(query)
	if query == "" || len(boxes) == 0 {
		return boxes, nil
	}
	if _, err := exec.LookPath("rg"); err != nil {
		return boxes, nil
	}
	args := []string{"--files-with-matches", "--no-messages"}
	args = append(args, buildShortlistRipgrepPattern(query)...)
	for _, box := range boxes {
		args = append(args, box.Path)
	}
	cmd := exec.Command("rg", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return nil, nil
		}
		return nil, fmt.Errorf("ripgrep shortlist: %w", err)
	}
	matched := map[string]struct{}{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		matched[filepath.Clean(line)] = struct{}{}
	}
	var filtered []Mailbox
	for _, box := range boxes {
		if _, ok := matched[filepath.Clean(box.Path)]; ok {
			filtered = append(filtered, box)
		}
	}
	return filtered, nil
}

func buildShortlistRipgrepPattern(q string) []string {
	tokens := uniqueStrings(strings.Fields(strings.TrimSpace(q)))
	if len(tokens) <= 1 {
		return buildRipgrepPattern(q)
	}
	args := []string{"--fixed-strings", "--ignore-case"}
	for _, token := range tokens {
		args = append(args, "-e", token)
	}
	return args
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func uniqueStrings(in []string) []string {
	m := map[string]struct{}{}
	var out []string
	for _, v := range in {
		if _, ok := m[v]; ok {
			continue
		}
		m[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func (a *App) showMail(profileName, folderLike, query, messageID, accountEmail string, limit int, thread bool) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	accountEmail = strings.ToLower(strings.TrimSpace(accountEmail))
	boxes, err := a.listMailboxes(profile)
	if err != nil {
		return err
	}
	dirToAccount, err := a.accountDirIndex(profile)
	if err != nil {
		return fmt.Errorf("account index: %w", err)
	}
	boxes, err = filterMailboxes(boxes, folderLike, accountEmail, dirToAccount)
	if err != nil {
		return err
	}

	if folderLike != "" && len(boxes) > 1 {
		// --folder is a substring filter, so a short name like "INBOX" fans out
		// across every account. Say so, or the first hit looks authoritative.
		log.Printf("info: --folder %q matched %d folders: %s", folderLike, len(boxes), describeFolders(boxes, 6))
	}
	queryLower := strings.ToLower(query)
	count := 0
	var skips skipTracker
	var threadSubject string
	var threadMsgs []struct {
		summary  MailSummary
		bodyText string
	}
	for _, target := range boxes {
		if limit > 0 && count >= limit && !thread {
			break
		}
		f, err := os.Open(target.Path)
		if err != nil {
			return err
		}
		reader := mbox.NewReader(f)
		targetAccount := accountEmail
		if targetAccount == "" {
			targetAccount = accountForPath(target.Path, dirToAccount)
		}
		for {
			if limit > 0 && count >= limit && !thread {
				break
			}
			msgReader, err := reader.NextMessage()
			if err == io.EOF {
				break
			}
			if err != nil {
				skips.note("show", target.Name, err)
				continue
			}
			summary, bodyText, err := parseMessageFull(msgReader, target.Name)
			if err != nil {
				continue
			}
			if targetAccount != "" {
				summary.Account = targetAccount
			}
			normSub := normalizeSubject(summary.Subject)
			if thread && threadSubject != "" {
				if normSub == threadSubject {
					threadMsgs = append(threadMsgs, struct {
						summary  MailSummary
						bodyText string
					}{summary: summary, bodyText: bodyText})
				}
				continue
			}
			if !matchMessage(summary, bodyText, queryLower, messageID) {
				continue
			}
			if thread {
				threadSubject = normSub
				threadMsgs = append(threadMsgs, struct {
					summary  MailSummary
					bodyText string
				}{summary: summary, bodyText: bodyText})
			} else {
				count++
				printFullMessage(summary, bodyText)
				fmt.Println(strings.Repeat("-", 80))
			}
		}
		_ = f.Close()
	}
	skips.report()
	if thread {
		if len(threadMsgs) == 0 {
			fmt.Printf("No matches in %d folder(s): %s.\n", len(boxes), describeFolders(boxes, 6))
			return nil
		}
		sort.Slice(threadMsgs, func(i, j int) bool {
			if threadMsgs[i].summary.When.IsZero() || threadMsgs[j].summary.When.IsZero() {
				return threadMsgs[i].summary.Date > threadMsgs[j].summary.Date
			}
			return threadMsgs[i].summary.When.Before(threadMsgs[j].summary.When)
		})
		if limit > 0 && len(threadMsgs) > limit {
			threadMsgs = threadMsgs[:limit]
		}
		for _, tm := range threadMsgs {
			printFullMessage(tm.summary, tm.bodyText)
			fmt.Println(strings.Repeat("-", 80))
		}
	} else if count == 0 {
		fmt.Printf("No matches in %d folder(s): %s.\n", len(boxes), describeFolders(boxes, 6))
	}
	return nil
}

func parseMessage(r io.Reader, folderName string) (MailSummary, string, error) {
	msg, err := mail.ReadMessage(io.LimitReader(r, maxMessageBytes))
	if err != nil {
		return MailSummary{}, "", err
	}
	decode := new(mime.WordDecoder)
	subject, _ := decode.DecodeHeader(msg.Header.Get("Subject"))
	from, _ := decode.DecodeHeader(msg.Header.Get("From"))
	dateHeader := msg.Header.Get("Date")
	when := dateHeader
	var whenTime time.Time
	if t, ok := parseDateFlexible(dateHeader); ok {
		when = t.In(time.Local).Format("2006-01-02 15:04")
		whenTime = t
	}

	bodyBytes, _ := io.ReadAll(io.LimitReader(msg.Body, maxMessageBytes))
	plain, alt := extractText(msg.Header, bodyBytes)
	bodyText := plain
	if bodyText == "" {
		bodyText = alt
	}
	snippet := firstNonEmptyLine(bodyText)
	searchText := strings.ToLower(strings.Join([]string{subject, from, dateHeader, bodyText}, " "))
	return MailSummary{
		Folder:    folderName,
		Subject:   strings.TrimSpace(subject),
		From:      strings.TrimSpace(from),
		Date:      when,
		MessageID: msg.Header.Get("Message-Id"),
		Snippet:   snippet,
		When:      whenTime,
	}, searchText, nil
}

func parseMessageFull(r io.Reader, folderName string) (MailSummary, string, error) {
	msg, err := mail.ReadMessage(io.LimitReader(r, maxMessageBytes))
	if err != nil {
		return MailSummary{}, "", err
	}
	decode := new(mime.WordDecoder)
	subject, _ := decode.DecodeHeader(msg.Header.Get("Subject"))
	from, _ := decode.DecodeHeader(msg.Header.Get("From"))
	dateHeader := msg.Header.Get("Date")
	when := dateHeader
	var whenTime time.Time
	if t, ok := parseDateFlexible(dateHeader); ok {
		when = t.In(time.Local).Format("2006-01-02 15:04")
		whenTime = t
	}
	bodyBytes, _ := io.ReadAll(io.LimitReader(msg.Body, maxMessageBytes))
	plain, alt := extractText(msg.Header, bodyBytes)
	bodyText := plain
	if bodyText == "" {
		bodyText = alt
	}
	return MailSummary{
		Folder:    folderName,
		Subject:   strings.TrimSpace(subject),
		From:      strings.TrimSpace(from),
		Date:      when,
		MessageID: msg.Header.Get("Message-Id"),
		Snippet:   firstNonEmptyLine(bodyText),
		When:      whenTime,
	}, bodyText, nil
}

func parseDateFlexible(dateHeader string) (time.Time, bool) {
	if dateHeader == "" {
		return time.Time{}, false
	}
	if t, err := mail.ParseDate(dateHeader); err == nil {
		return t, true
	}
	norm := normalizeTZOffset(dateHeader)
	if t, err := mail.ParseDate(norm); err == nil {
		return t, true
	}
	// Try without timezone if offset was invalid.
	noTZ := regexp.MustCompile(`([+-]\d{4})`).ReplaceAllString(norm, "")
	layouts := []string{
		"Mon, 2 Jan 2006 15:04:05",
		"2 Jan 2006 15:04:05",
		"Mon, 2 Jan 2006 15:04",
		"2 Jan 2006 15:04",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, strings.TrimSpace(noTZ)); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

func normalizeTZOffset(s string) string {
	re := regexp.MustCompile(`([+-])(\d{2})(\d{2})`)
	return re.ReplaceAllStringFunc(s, func(m string) string {
		sign := m[0:1]
		hh, _ := strconv.Atoi(m[1:3])
		mm, _ := strconv.Atoi(m[3:5])
		if hh > 23 {
			hh = 23
		}
		if mm > 59 {
			mm = 59
		}
		return fmt.Sprintf("%s%02d%02d", sign, hh, mm)
	})
}

func hasMissingDates(msgs []MailSummary) bool {
	for _, m := range msgs {
		if m.When.IsZero() {
			return true
		}
	}
	return false
}

func normalizeSubject(sub string) string {
	s := strings.ToLower(strings.TrimSpace(sub))
	for {
		switch {
		case strings.HasPrefix(s, "re:"):
			s = strings.TrimSpace(strings.TrimPrefix(s, "re:"))
		case strings.HasPrefix(s, "fwd:"):
			s = strings.TrimSpace(strings.TrimPrefix(s, "fwd:"))
		case strings.HasPrefix(s, "fw:"):
			s = strings.TrimSpace(strings.TrimPrefix(s, "fw:"))
		default:
			return s
		}
	}
}

func printFullMessage(m MailSummary, body string) {
	fmt.Printf("From: %s\n", m.From)
	fmt.Printf("Subject: %s\n", m.Subject)
	fmt.Printf("Date: %s\n", m.Date)
	if m.Account != "" {
		fmt.Printf("Account: %s\n", m.Account)
	}
	fmt.Printf("Folder: %s\n", m.Folder)
	if m.MessageID != "" {
		fmt.Printf("Message-ID: %s\n", m.MessageID)
	}
	fmt.Println()
	fmt.Println(body)
}

func firstNonEmptyLine(body string) string {
	scanner := bufio.NewScanner(strings.NewReader(body))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			return truncate(line, 160)
		}
	}
	return ""
}

func extractText(h mail.Header, body []byte) (plain string, fallback string) {
	ctype := h.Get("Content-Type")
	mediaType, params, err := mime.ParseMediaType(ctype)
	if err != nil || mediaType == "" {
		mediaType = "text/plain"
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return "", ""
		}
		mr := multipart.NewReader(bytes.NewReader(body), boundary)
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				break
			}
			partBody, _ := io.ReadAll(io.LimitReader(part, maxPartBytes))
			pPlain, pFallback := extractText(mail.Header(part.Header), partBody)
			if pPlain != "" && plain == "" {
				plain = pPlain
			}
			if pFallback != "" && fallback == "" {
				fallback = pFallback
			}
			if plain != "" && fallback != "" {
				continue
			}
		}
		return plain, fallback
	}

	disposition := strings.ToLower(h.Get("Content-Disposition"))
	if strings.Contains(disposition, "attachment") {
		return "", ""
	}

	if !strings.HasPrefix(mediaType, "text/plain") && !strings.HasPrefix(mediaType, "text/html") {
		return "", ""
	}

	ctEncoding := h.Get("Content-Transfer-Encoding")
	decoded, err := decodeBodyContent(ctEncoding, body)
	if err != nil {
		decoded = body
	}
	if cs, ok := params["charset"]; ok && !strings.EqualFold(cs, "utf-8") {
		if conv, err := convertCharset(decoded, cs); err == nil {
			decoded = conv
		}
	}
	text := string(decoded)
	if strings.HasPrefix(mediaType, "text/html") {
		return "", htmlToText(text)
	}
	return text, ""
}

func decodeBodyContent(encoding string, body []byte) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		r := base64.NewDecoder(base64.StdEncoding, bytes.NewReader(body))
		return io.ReadAll(io.LimitReader(r, maxPartBytes))
	case "quoted-printable":
		r := quotedprintable.NewReader(bytes.NewReader(body))
		return io.ReadAll(io.LimitReader(r, maxPartBytes))
	default:
		return body, nil
	}
}

func convertCharset(body []byte, cs string) ([]byte, error) {
	r, err := charset.NewReaderLabel(cs, bytes.NewReader(body))
	if err != nil {
		return body, err
	}
	return io.ReadAll(io.LimitReader(r, maxPartBytes))
}

func htmlToText(htmlBody string) string {
	z := html.NewTokenizer(strings.NewReader(htmlBody))
	var b strings.Builder
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		if tt == html.TextToken {
			t := strings.TrimSpace(string(z.Text()))
			if t != "" {
				if b.Len() > 0 {
					b.WriteByte(' ')
				}
				b.WriteString(t)
			}
		}
	}
	words := strings.Fields(b.String())
	return strings.Join(words, " ")
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func byteSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 5 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
