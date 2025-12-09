package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"flag"
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
	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"regexp"
	"text/tabwriter"
)

type App struct {
	Root string
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

func mailMain(args []string) {
	if len(args) == 0 {
		mailUsage()
		return
	}
	app := newApp()
	switch args[0] {
	case "profiles":
		cmd := flag.NewFlagSet("profiles", flag.ExitOnError)
		cmd.Parse(args[1:])
		if err := app.printProfiles(); err != nil {
			log.Fatalf("profiles: %v", err)
		}
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
		cmd.Parse(args[1:])
		pos := cmd.Args()
		if len(pos) < 1 {
			log.Fatalf("recent: folder name required (e.g. Inbox)")
		}
		if err := app.recent(pos[0], *profileName, *limit, *query); err != nil {
			log.Fatalf("recent: %v", err)
		}
	case "search":
		cmd := flag.NewFlagSet("search", flag.ExitOnError)
		profileName := cmd.String("profile", "", "profile name or path")
		folderLike := cmd.String("folder", "", "restrict to folders containing this name")
		limit := cmd.Int("limit", 25, "max results across folders")
		rawGrep := cmd.Bool("raw", false, "use ripgrep for faster raw text search (no parsing/snippets)")
		since := cmd.String("since", "", "only include messages on/after YYYY-MM-DD")
		sinceShort := cmd.String("ds", "", "alias for --since")
		till := cmd.String("till", "", "only include messages on/before YYYY-MM-DD")
		tillShort := cmd.String("dt", "", "alias for --till")
		maxScan := cmd.Int("max-messages", 0, "stop after scanning this many messages per folder (0 = all)")
		account := cmd.String("account", "", "filter by account email")
		accountShort := cmd.String("ac", "", "alias for --account")
		noFancy := cmd.Bool("no-fancy", false, "plain output (no table)")
		allScan := cmd.Bool("all", false, "scan full folders (disable default cap)")
		noIndex := cmd.Bool("no-index", false, "skip index cache, scan mbox directly")
		tailCount := cmd.Int("tail", 0, "keep only last N messages per folder (0 = from start)")
		fuzzy := cmd.Bool("fuzzy", false, "fuzzy token match (all tokens must appear)")
		cmd.Parse(args[1:])
		pos := cmd.Args()
		if len(pos) < 1 {
			log.Fatalf("search: query required")
		}
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
		maxMsgs := *maxScan
		if *allScan {
			maxMsgs = 0
		}
		if err := app.search(pos[0], *profileName, *folderLike, acct, *limit, *rawGrep, *noFancy, sinceTime, tillTime, maxMsgs, *noIndex, *tailCount, false, false, *fuzzy); err != nil {
			log.Fatalf("search: %v", err)
		}
	case "compose":
		cmd := flag.NewFlagSet("compose", flag.ExitOnError)
		to := cmd.String("to", "", "comma-separated recipients")
		cc := cmd.String("cc", "", "cc recipients")
		subject := cmd.String("subject", "", "subject")
		body := cmd.String("body", "", "body text")
		openComposer := cmd.Bool("open", true, "open Thunderbird compose window")
		sendNow := cmd.Bool("send", false, "attempt to auto-send without GUI (-send)")
		cmd.Parse(args[1:])
		if *to == "" {
			log.Fatalf("compose: --to is required")
		}
		if !*openComposer && !*sendNow {
			log.Fatalf("compose: nothing to do (set --open or --send)")
		}
		if err := app.compose(*to, *cc, *subject, *body, *openComposer, *sendNow); err != nil {
			log.Fatalf("compose: %v", err)
		}
	case "help", "-h", "--help":
		mailUsage()
	case "show":
		cmd := flag.NewFlagSet("show", flag.ExitOnError)
		profileName := cmd.String("profile", "", "profile name or path")
		folderLike := cmd.String("folder", "", "folder name/substring to search")
		query := cmd.String("query", "", "substring match against subject/from/body")
		limit := cmd.Int("limit", 1, "max messages to display")
		account := cmd.String("account", "", "filter by account email")
		accountShort := cmd.String("ac", "", "alias for --account")
		thread := cmd.Bool("thread", false, "if set, show entire thread (same subject) after first match")
		cmd.Parse(args[1:])
		if *folderLike == "" || *query == "" {
			log.Fatalf("show: --folder and --query are required")
		}
		acct := *account
		if acct == "" {
			acct = *accountShort
		}
		if err := app.showMail(*profileName, *folderLike, *query, acct, *limit, *thread); err != nil {
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
	log.Println("  recent <folder> [--query q]          show recent messages from a folder")
	log.Println("  search <query> [--since/--ds YYYY-MM-DD] [--till/--dt YYYY-MM-DD] [--account/--ac email] [--max-messages N|--all] [--tail N] [--raw] [--no-fancy] [--no-index]")
	log.Println("  index [--profile p] [--folder f] [--account/--ac email] [--tail N]   prebuild cache for faster search")
	log.Println("  show --folder <name> --query <text> [--profile p] [--account/--ac email] [--limit N] [--thread]  print full messages matching substring (optionally whole thread)")
	log.Println("  compose --to ...                     open/send via Thunderbird composer")
}

func newApp() *App {
	root := os.Getenv("THUNDERBIRD_HOME")
	if root == "" {
		root = filepath.Join(os.Getenv("HOME"), ".thunderbird")
	}
	return &App{Root: root}
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

func (a *App) recent(folder, profileName string, limit int, query string) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	boxes, err := a.listMailboxes(profile)
	if err != nil {
		return err
	}
	box, ok := findMailbox(boxes, folder)
	if !ok {
		return fmt.Errorf("folder %s not found; try `tb mail folders --profile %s`", folder, profile.Name)
	}
	messages, err := readMailboxRecent(box, limit, query)
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		fmt.Println("No messages found.")
		return nil
	}
	fmt.Printf("Recent from %s (profile %s):\n", box.Name, profile.Name)
	for i := len(messages) - 1; i >= 0; i-- {
		m := messages[i]
		fmt.Printf("%-16s | %-40s | %-40s | %s\n", m.Date, truncate(m.From, 38), truncate(m.Subject, 60), truncate(m.Snippet, 80))
	}
	return nil
}

func (a *App) search(query, profileName, folderLike, accountEmail string, limit int, useRaw, noFancy bool, since, till time.Time, maxMessages int, noIndex bool, tailCount int, includeTrash, includeSpam bool, fuzzy bool) error {
	_ = includeTrash
	_ = includeSpam
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	accountEmail = strings.ToLower(strings.TrimSpace(accountEmail))
	boxes, err := a.listMailboxes(profile)
	if err != nil {
		return err
	}

	// Account filter -> reduce mailboxes to account directories.
	dirToAccount := map[string]string{}
	var accountDirs []string
	if accountEmail != "" {
		idx, err := a.loadAccountDirIndex(profile)
		if err != nil {
			return fmt.Errorf("account index: %w", err)
		}
		accountDirs = idx[accountEmail]
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
	}

	var filtered []Mailbox
	for _, b := range boxes {
		if folderLike == "" {
			filtered = append(filtered, b)
			continue
		}
		needle := strings.ToLower(folderLike)
		if strings.Contains(strings.ToLower(b.Name), needle) || strings.Contains(strings.ToLower(filepath.Base(b.Name)), needle) {
			filtered = append(filtered, b)
		}
	}
	if len(filtered) == 0 {
		return fmt.Errorf("no folders match %q", folderLike)
	}
	if useRaw {
		return rawSearch(query, filtered, limit, noFancy)
	}

	var cache *IndexFile
	if !noIndex {
		cache, _ = loadIndex(indexPath(profile))
		if cache == nil {
			cache = &IndexFile{Folders: map[string]FolderIndex{}}
		}
	}
	changed := false
	matcher := makeMatcher(query, fuzzy)
	var hits []MailSummary
	seenKeys := map[string]struct{}{}
	for _, b := range filtered {
		targetAccount := accountEmail
		if targetAccount == "" {
			targetAccount = accountForPath(b.Path, dirToAccount)
		}

		var folderIdx *FolderIndex
		useCache := false
		if cache != nil && !noIndex {
			if fi, err := os.Stat(b.Path); err == nil {
				if entry, ok := cache.Folders[b.Path]; ok && entry.ModTime == fi.ModTime().Unix() && entry.Size == fi.Size() && entry.Complete && !hasMissingDates(entry.Messages) {
					folderIdx = &entry
					useCache = true
				}
			}
		}

		filterMsgs := func(msgs []MailSummary) []MailSummary {
			var out []MailSummary
			for _, im := range msgs {
				if im.Account == "" {
					if targetAccount != "" {
						im.Account = targetAccount
					} else {
						if acct := accountForPath(b.Path, dirToAccount); acct != "" {
							im.Account = acct
						}
					}
				}
				if im.Search == "" {
					im.Search = strings.ToLower(strings.Join([]string{im.Subject, im.From, im.Snippet}, " "))
				}
				if !since.IsZero() && !im.When.IsZero() && im.When.Before(since) {
					continue
				}
				if !till.IsZero() && !im.When.IsZero() && im.When.After(till) {
					continue
				}
				if accountEmail != "" && strings.ToLower(im.Account) != accountEmail {
					continue
				}
				if matcher(im.Search) {
					out = append(out, im)
				}
			}
			return out
		}

		var more []MailSummary
		if useCache && folderIdx != nil {
			more = filterMsgs(folderIdx.Messages)
		}

		needRefresh := noIndex || !useCache || len(more) == 0 || (limit > 0 && len(more) < limit) || (folderIdx != nil && !folderIdx.Complete)
		if needRefresh {
			if noIndex {
				live, err := searchMailbox(b, matcher, 0, since, till, maxMessages, targetAccount, tailCount)
				if err != nil {
					log.Printf("warn: search %s: %v", b.Name, err)
				} else {
					more = live
				}
			} else {
				idxEntry, err := a.buildFolderIndex(b, targetAccount, maxMessages, tailCount)
				if err != nil {
					log.Printf("warn: index %s: %v", b.Name, err)
				} else {
					cache.Folders[b.Path] = idxEntry
					folderIdx = &idxEntry
					more = filterMsgs(idxEntry.Messages)
					changed = true
				}
			}
		}

		for _, m := range more {
			key := fmt.Sprintf("%s|%s|%s|%s", m.MessageID, m.Subject, m.Date, m.Folder)
			if key == "|||" {
				// Fallback key on missing metadata.
				key = fmt.Sprintf("%s|%s", m.Date, m.Snippet)
			}
			if _, ok := seenKeys[key]; ok {
				continue
			}
			seenKeys[key] = struct{}{}
			hits = append(hits, m)
		}
	}
	if len(hits) == 0 {
		fmt.Println("No matches.")
		return nil
	}
	if cache != nil && changed {
		_ = saveIndex(indexPath(profile), cache)
	}
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

	if noFancy {
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
	fmt.Fprintf(w, "Date\tFolder\tFrom\tSubject\tSnippet\n")
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

func (a *App) compose(to, cc, subject, body string, openComposer, sendNow bool) error {
	if _, err := exec.LookPath("thunderbird"); err != nil {
		return fmt.Errorf("thunderbird binary not found in PATH")
	}
	var parts []string
	parts = append(parts, fmt.Sprintf("to=%s", to))
	if cc != "" {
		parts = append(parts, fmt.Sprintf("cc=%s", cc))
	}
	if subject != "" {
		parts = append(parts, fmt.Sprintf("subject=%s", subject))
	}
	if body != "" {
		parts = append(parts, fmt.Sprintf("body=%s", body))
	}
	composeArg := strings.Join(parts, ",")
	args := []string{"-compose", composeArg}
	if sendNow {
		args = append(args, "-send")
	}
	cmd := exec.Command("thunderbird", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if !openComposer && sendNow {
		// Send without GUI; Thunderbird still needs to start to process the compose command.
		return cmd.Run()
	}
	if openComposer {
		return cmd.Run()
	}
	return nil
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
		for _, p := range profiles {
			if p.Default {
				return p, nil
			}
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
		if dir == "" {
			dirRel := prefs[fmt.Sprintf("mail.server.%s.directory-rel", server)]
			if strings.HasPrefix(dirRel, "[ProfD]") {
				dir = filepath.Join(p.AbsolutePath, filepath.FromSlash(strings.TrimPrefix(dirRel, "[ProfD]")))
			} else if dirRel != "" {
				dir = filepath.Clean(dirRel)
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

func parsePrefs(path string) (map[string]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	re := regexp.MustCompile(`user_pref\(\"([^\"]+)\",\s*\"(.*)\"\);`)
	m := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		matches := re.FindStringSubmatch(line)
		if len(matches) == 3 {
			m[matches[1]] = matches[2]
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

func accountForPath(path string, dirToAccount map[string]string) string {
	for dir, acct := range dirToAccount {
		if strings.HasPrefix(path, dir) {
			return acct
		}
	}
	return ""
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
	reader := mbox.NewReader(f)
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

func (a *App) showMail(profileName, folderLike, query, accountEmail string, limit int, thread bool) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	accountEmail = strings.ToLower(strings.TrimSpace(accountEmail))
	boxes, err := a.listMailboxes(profile)
	if err != nil {
		return err
	}
	var target Mailbox
	found := false
	for _, b := range boxes {
		needle := strings.ToLower(folderLike)
		if strings.Contains(strings.ToLower(b.Name), needle) || strings.Contains(strings.ToLower(filepath.Base(b.Name)), needle) {
			target = b
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("no folders match %q", folderLike)
	}

	if accountEmail != "" {
		idx, err := a.loadAccountDirIndex(profile)
		if err != nil {
			return fmt.Errorf("account index: %w", err)
		}
		dirs := idx[accountEmail]
		ok := false
		for _, d := range dirs {
			if strings.HasPrefix(target.Path, d) {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("folder %s not in account %s", target.Name, accountEmail)
		}
	}

	queryLower := strings.ToLower(query)
	f, err := os.Open(target.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	reader := mbox.NewReader(f)
	count := 0
	var threadSubject string
	var threadMsgs []struct {
		summary  MailSummary
		bodyText string
	}
	for {
		if limit > 0 && count >= limit {
			break
		}
		msgReader, err := reader.NextMessage()
		if err == io.EOF {
			break
		}
		if err != nil {
			log.Printf("warn: %s: %v", target.Name, err)
			continue
		}
		summary, bodyText, err := parseMessageFull(msgReader, target.Name)
		if err != nil {
			continue
		}
		if accountEmail != "" {
			summary.Account = accountEmail
		}
		blob := strings.ToLower(strings.Join([]string{summary.Subject, summary.From, bodyText}, " "))
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
		if !strings.Contains(blob, queryLower) {
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
	if thread {
		if len(threadMsgs) == 0 {
			fmt.Println("No matches.")
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
		fmt.Println("No matches.")
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
