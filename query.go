package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-mbox"
)

// `tb q` — one command that answers "find me the mail about X".
//
// The other search commands each solve part of the problem and leave the caller
// to compose them: pick a profile, pick folders, decide whether to refresh,
// decide whether to widen when nothing matched. An agent pays a round trip for
// every one of those decisions, and a wrong guess looks identical to "no such
// mail". `q` makes the decisions itself, then reports what it decided.

type queryStrategy struct {
	name    string
	ftsExpr string
	note    string
}

// queryRequest carries everything one `tb q` invocation needs. It is a struct
// because the option count crossed the point where positional arguments stop
// being readable.
type queryRequest struct {
	Query      string
	Profile    string
	Account    string
	Limit      int
	Since      time.Time
	Till       time.Time
	JSON       bool
	IncludeAll bool
	NoRefresh  bool
	WithBody   bool
	BodyChars  int
	Thread     bool
	Important  bool
}

// agentQuery searches everything and widens until it finds something, saying
// which strategy produced the answer.
func (a *App) agentQuery(req queryRequest) error {
	query, profileName, accountEmail := req.Query, req.Profile, req.Account
	limit, since, till := req.Limit, req.Since, req.Till
	asJSON, noRefresh := req.JSON, req.NoRefresh
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	accountEmail = strings.ToLower(strings.TrimSpace(accountEmail))
	var notes []string

	store, err := openStore()
	if err != nil {
		return fmt.Errorf("open search store: %w", err)
	}
	defer store.Close()
	ctx := context.Background()

	cacheAge := ""
	if ts, mErr := store.GetMeta(ctx, fmt.Sprintf("last_scan.%s", profile.Name)); mErr == nil && ts != "" {
		if last, pErr := time.Parse(time.RFC3339, ts); pErr == nil {
			cacheAge = time.Since(last).Round(time.Minute).String()
			// Refresh on staleness by default: an agent asking for mail almost
			// always means "including what just arrived".
			if !noRefresh && autoRefreshWindow() > 0 && time.Since(last) > autoRefreshWindow() {
				if err := a.ingestProfile(ctx, store, profile, ingestOptions{syncFirst: true}); err != nil {
					notes = append(notes, fmt.Sprintf("refresh failed, results may be stale: %v", err))
				} else {
					notes = append(notes, "refreshed the cache before searching")
					cacheAge = "0s"
				}
			}
		}
	}

	count, _ := store.CountMessages(ctx, profile.Name)
	if count == 0 {
		notes = append(notes, "cache empty; scanned mailbox files directly")
		return a.queryDirect(profile, query, accountEmail, limit, since, till, asJSON, notes)
	}

	// A window with no search terms is a legitimate question ("what arrived
	// today"), so browse by time instead of demanding a query.
	if strings.TrimSpace(query) == "" {
		if since.IsZero() && till.IsZero() {
			return fmt.Errorf("give a query, a time window (--today, --since 7d), or both")
		}
		hits, sErr := store.Search(ctx, queryOptions{
			account: accountEmail,
			since:   since,
			till:    till,
			limit:   0,
			profile: profile.Name,
		})
		if sErr != nil {
			return fmt.Errorf("browse by time: %w", sErr)
		}
		notes = append(notes, "no search terms: listing everything in the requested window")
		return a.finishQuery(profile, req, hits, notes, cacheAge, "time-window")
	}

	// Progressive widening. Each step is strictly more permissive than the
	// last, and the one that produced the answer is reported so a caller can
	// judge how literal the match was.
	strategies := []queryStrategy{
		{name: "exact", ftsExpr: ftsExpression(query, "AND", false)},
		{name: "prefix", ftsExpr: ftsExpression(query, "AND", true),
			note: "no exact-token match; matched on token prefixes"},
		{name: "any-token", ftsExpr: ftsExpression(query, "OR", true),
			note: "no message contained every term; showing best partial matches"},
	}

	var hits []MailSummary
	used := ""
	for _, s := range strategies {
		if strings.TrimSpace(s.ftsExpr) == "" {
			continue
		}
		found, sErr := store.Search(ctx, queryOptions{
			ftsExpr: s.ftsExpr,
			account: accountEmail,
			since:   since,
			till:    till,
			limit:   limit,
			profile: profile.Name,
		})
		if sErr != nil {
			notes = append(notes, fmt.Sprintf("store search failed (%v); scanned mailbox files directly", sErr))
			return a.queryDirect(profile, query, accountEmail, limit, since, till, asJSON, notes)
		}
		if len(found) > 0 {
			hits = found
			used = s.name
			if s.note != "" {
				notes = append(notes, s.note)
			}
			break
		}
	}

	// Date filters are the most common reason a correct query returns nothing.
	if len(hits) == 0 && (!since.IsZero() || !till.IsZero()) {
		found, sErr := store.Search(ctx, queryOptions{
			ftsExpr: ftsExpression(query, "AND", true),
			account: accountEmail,
			limit:   limit,
			profile: profile.Name,
		})
		if sErr == nil && len(found) > 0 {
			hits = found
			used = "ignored-date-filter"
			notes = append(notes, "no match inside the requested date range; these are outside it")
		}
	}

	return a.finishQuery(profile, req, hits, notes, cacheAge, used)
}

// finishQuery applies ranking, thread expansion and body hydration, then emits.
// Shared so a time-window browse and a keyword search behave identically.
func (a *App) finishQuery(profile Profile, req queryRequest, hits []MailSummary, notes []string, cacheAge, strategy string) error {
	if !req.IncludeAll {
		hits = demoteNoise(hits)
	}

	if req.Important {
		accounts := a.profileAccounts(profile)
		hits = rankByImportance(hits, accounts)
		notes = append(notes, "ranked by importance (direct/thread/deadline signals up, bulk down)")
	} else {
		sortHits(hits, req.Query)
	}

	if req.Thread && len(hits) > 0 {
		expanded, tErr := a.expandThread(profile, hits[0])
		if tErr == nil && len(expanded) > 1 {
			hits = expanded
			notes = append(notes, fmt.Sprintf("expanded the top hit into its %d-message thread", len(expanded)))
		} else if tErr != nil {
			notes = append(notes, fmt.Sprintf("thread expansion failed: %v", tErr))
		}
	}

	if req.Limit > 0 && len(hits) > req.Limit {
		hits = hits[:req.Limit]
	}

	if req.WithBody {
		if err := a.hydrateBodies(profile, hits, req.BodyChars); err != nil {
			notes = append(notes, fmt.Sprintf("body hydration incomplete: %v", err))
		}
	}
	if strategy != "" {
		notes = append(notes, "match strategy: "+strategy)
	}

	scope := scopeJSON{
		Profile:  profile.Name,
		Account:  req.Account,
		CacheAge: cacheAge,
	}
	// Report the real folder count. The cache query is not folder-scoped, so
	// reporting 0 here would understate the scope and make an empty result look
	// like it had searched nothing.
	if boxes, bErr := a.listMailboxes(profile); bErr == nil {
		scope.Folders = len(boxes)
	}
	if !req.Since.IsZero() {
		scope.Since = req.Since.Format("2006-01-02 15:04")
	}
	if !req.Till.IsZero() {
		scope.Till = req.Till.Format("2006-01-02 15:04")
	}
	return emitQueryResult(req.Query, hits, scope, notes, req.JSON)
}

// rankByImportance orders a window of mail so what a person needs to act on
// surfaces above the newsletters, and records why on each message.
func rankByImportance(hits []MailSummary, accounts map[string]bool) []MailSummary {
	type scored struct {
		m MailSummary
		v importanceVerdict
	}
	list := make([]scored, 0, len(hits))
	for _, h := range hits {
		v := scoreImportance(h, accounts)
		h.ImportanceScore = v.Score
		h.ImportanceWhy = v.Reasons
		list = append(list, scored{m: h, v: v})
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].v.Score != list[j].v.Score {
			return list[i].v.Score > list[j].v.Score
		}
		return list[i].m.When.After(list[j].m.When)
	})
	out := make([]MailSummary, 0, len(list))
	for _, s := range list {
		out = append(out, s.m)
	}
	return out
}

// queryDirect is the cold-cache path: scan the mailbox files themselves.
func (a *App) queryDirect(profile Profile, query, accountEmail string, limit int, since, till time.Time, asJSON bool, notes []string) error {
	boxes, dirToAccount, err := a.searchBoxes(profile, "", accountEmail)
	if err != nil {
		return err
	}
	hits, err := directSearchMailboxes(profile.Name, boxes, dirToAccount, query, limit, since, till, true)
	if err != nil {
		return err
	}
	hits = demoteNoise(hits)
	sortHits(hits, query)
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return emitQueryResult(query, hits, scopeJSON{
		Profile:     profile.Name,
		Account:     accountEmail,
		Folders:     len(boxes),
		FolderNames: folderNames(boxes, 8),
	}, notes, asJSON)
}

// demoteNoise drops nothing but pushes trash/spam copies below real mail, so a
// duplicate sitting in Junk cannot outrank the original.
func demoteNoise(hits []MailSummary) []MailSummary {
	var primary, noise []MailSummary
	for _, h := range hits {
		if h.FolderTag == "trash" || h.FolderTag == "spam" {
			noise = append(noise, h)
			continue
		}
		primary = append(primary, h)
	}
	return append(primary, noise...)
}

func emitQueryResult(query string, hits []MailSummary, scope scopeJSON, notes []string, asJSON bool) error {
	if asJSON {
		return emitJSON(resultJSON{
			Query:    query,
			Count:    len(hits),
			Messages: toMessagesJSON(hits),
			Scope:    scope,
			Notes:    notes,
		})
	}
	for _, n := range notes {
		log.Printf("info: %s", n)
	}
	if len(hits) == 0 {
		fmt.Printf("No matches for %q in profile %s.\n", query, scope.Profile)
		fmt.Println("Nothing was filtered out by folder: q searches every folder, including Junk and Trash.")
		return nil
	}
	return printSortedHits(hits, 0, true)
}

// stdoutIsTerminal reports whether stdout is a terminal. When it is not, the
// caller is a script or an agent, so JSON is the better default.
func stdoutIsTerminal() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// hydrateBodies fills in message bodies for results already selected.
//
// "Find it, then show me what it says" was always two calls; the second was
// pure overhead because tb already knew exactly which file and message to open.
// Bodies are read from the mailbox files, bounded by maxChars so a long thread
// cannot blow up the response.
func (a *App) hydrateBodies(profile Profile, hits []MailSummary, maxChars int) error {
	if len(hits) == 0 {
		return nil
	}
	if maxChars <= 0 {
		maxChars = 4000
	}
	// Group the wanted ids by folder so each mailbox file is opened once.
	want := map[string]map[string]int{} // folder -> message-id -> index
	for i, h := range hits {
		id := strings.TrimSpace(h.MessageID)
		if id == "" {
			continue
		}
		if want[h.Folder] == nil {
			want[h.Folder] = map[string]int{}
		}
		want[h.Folder][id] = i
	}

	boxes, err := a.listMailboxes(profile)
	if err != nil {
		return err
	}
	byName := map[string]Mailbox{}
	for _, b := range boxes {
		byName[b.Name] = b
	}

	var missing int
	for folder, ids := range want {
		box, ok := byName[folder]
		if !ok {
			missing += len(ids)
			continue
		}
		f, err := os.Open(box.Path)
		if err != nil {
			missing += len(ids)
			continue
		}
		reader := mbox.NewReader(f)
		remaining := len(ids)
		for remaining > 0 {
			msgReader, err := reader.NextMessage()
			if err != nil {
				break
			}
			summary, body, err := parseMessageFull(msgReader, folder)
			if err != nil {
				continue
			}
			idx, wanted := ids[strings.TrimSpace(summary.MessageID)]
			if !wanted {
				continue
			}
			hits[idx].Body = truncate(strings.TrimSpace(body), maxChars)
			remaining--
		}
		missing += remaining
		_ = f.Close()
	}
	if missing > 0 {
		return fmt.Errorf("%d message(s) could not be read from disk", missing)
	}
	return nil
}

// expandThread returns every message sharing the seed's normalised subject,
// oldest first — the shape you want when someone "replied back" and the whole
// exchange matters, not just the newest line.
func (a *App) expandThread(profile Profile, seed MailSummary) ([]MailSummary, error) {
	store, err := openStore()
	if err != nil {
		return nil, err
	}
	defer store.Close()

	subject := normalizeSubject(seed.Subject)
	if strings.TrimSpace(subject) == "" {
		return []MailSummary{seed}, nil
	}
	candidates, err := store.Search(context.Background(), queryOptions{
		ftsExpr: ftsExpression(subject, "AND", false),
		profile: profile.Name,
		limit:   0,
	})
	if err != nil {
		return nil, err
	}
	var thread []MailSummary
	seen := map[string]bool{}
	for _, c := range candidates {
		if normalizeSubject(c.Subject) != subject {
			continue
		}
		id := strings.TrimSpace(c.MessageID)
		if id != "" && seen[id] {
			continue
		}
		seen[id] = true
		thread = append(thread, c)
	}
	if len(thread) == 0 {
		return []MailSummary{seed}, nil
	}
	sort.SliceStable(thread, func(i, j int) bool { return thread[i].When.Before(thread[j].When) })
	return thread, nil
}
