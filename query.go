package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
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

// agentQuery searches everything and widens until it finds something, saying
// which strategy produced the answer.
func (a *App) agentQuery(query, profileName, accountEmail string, limit int, since, till time.Time, asJSON bool, includeAll bool, noRefresh bool) error {
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

	if !includeAll {
		hits = demoteNoise(hits)
	}
	sortHits(hits, query)
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	if used != "" {
		notes = append(notes, "match strategy: "+used)
	}

	scope := scopeJSON{
		Profile:  profile.Name,
		Account:  accountEmail,
		CacheAge: cacheAge,
	}
	if !since.IsZero() {
		scope.Since = since.Format("2006-01-02")
	}
	if !till.IsZero() {
		scope.Till = till.Format("2006-01-02")
	}
	return emitQueryResult(query, hits, scope, notes, asJSON)
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
