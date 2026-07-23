package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// Output shaped for the tool's primary consumer: a coding agent, not a human
// at a terminal.
//
// The human-facing table truncates every column and omits the Message-ID
// entirely, which means a search result cannot be fed back into `tb read`
// without guessing at `--query` again. That single gap is responsible for most
// of the "search failed, hand-hold me" loops. JSON output fixes it by being
// complete, untruncated, and stable enough to address a message by identity.

// messageJSON is the stable wire shape. Field names are snake_case and are
// treated as API: renaming one breaks every caller that parses tb.
type messageJSON struct {
	MessageID string `json:"message_id"`
	Subject   string `json:"subject"`
	From      string `json:"from"`
	Account   string `json:"account"`
	Folder    string `json:"folder"`
	FolderTag string `json:"folder_tag,omitempty"`
	Date      string `json:"date"`
	DateRaw   string `json:"date_raw,omitempty"`
	Snippet   string `json:"snippet,omitempty"`
	Profile   string `json:"profile,omitempty"`
	// Read is the exact command that retrieves this message in full. Emitting
	// it removes a whole class of malformed follow-up calls.
	Read string `json:"read,omitempty"`
}

// resultJSON wraps hits with the scope they were found in. An agent must be
// able to tell "no such mail" from "searched the wrong place" without asking a
// human, so the scope travels with the result.
type resultJSON struct {
	Query    string        `json:"query,omitempty"`
	Count    int           `json:"count"`
	Messages []messageJSON `json:"messages"`
	Scope    scopeJSON     `json:"scope"`
	// Notes carry anything that changed the meaning of the result: a widened
	// search, a swapped profile, a stale cache.
	Notes []string `json:"notes,omitempty"`
}

type scopeJSON struct {
	Profile     string   `json:"profile"`
	Folders     int      `json:"folders_searched"`
	FolderNames []string `json:"folder_names,omitempty"`
	Account     string   `json:"account,omitempty"`
	Since       string   `json:"since,omitempty"`
	Till        string   `json:"till,omitempty"`
	CacheAge    string   `json:"cache_age,omitempty"`
}

func toMessageJSON(m MailSummary) messageJSON {
	date := m.Date
	if !m.When.IsZero() {
		date = m.When.Format(time.RFC3339)
	}
	out := messageJSON{
		MessageID: strings.TrimSpace(m.MessageID),
		Subject:   m.Subject,
		From:      m.From,
		Account:   m.Account,
		Folder:    m.Folder,
		FolderTag: m.FolderTag,
		Date:      date,
		Snippet:   m.Snippet,
		Profile:   m.Profile,
	}
	if !m.When.IsZero() && m.Date != "" {
		out.DateRaw = m.Date
	}
	if out.MessageID != "" {
		out.Read = fmt.Sprintf("tb read --message-id %q", out.MessageID)
	}
	return out
}

func toMessagesJSON(msgs []MailSummary) []messageJSON {
	out := make([]messageJSON, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toMessageJSON(m))
	}
	return out
}

// emitJSON writes one JSON document to stdout. Indented because these results
// are read by a model as often as they are parsed by code, and indentation
// costs little next to the message bodies.
func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// wantJSON reports whether output should be JSON. Explicit --json always wins;
// otherwise TB_JSON=1 turns it on globally so an agent can set it once per
// session instead of remembering the flag on every call.
func wantJSON(flagSet bool) bool {
	if flagSet {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TB_JSON"))) {
	case "", "0", "false", "no":
		return false
	}
	return true
}

// folderNames lists mailbox names for a scope report, bounded so the JSON stays
// readable when a short --folder matches everything.
func folderNames(boxes []Mailbox, max int) []string {
	out := make([]string, 0, len(boxes))
	for i, b := range boxes {
		if i >= max {
			out = append(out, fmt.Sprintf("... (+%d more)", len(boxes)-max))
			break
		}
		out = append(out, b.Name)
	}
	return out
}
