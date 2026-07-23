package main

import (
	"strings"
	"testing"
	"time"
)

func TestFTSExpressionModes(t *testing.T) {
	t.Parallel()

	if got := ftsExpression("parcel signals", "AND", false); got != `"parcel" AND "signals"` {
		t.Fatalf("exact AND = %q", got)
	}
	// Prefix matching is what makes "invoic" find "invoices"; exact-token-only
	// matching is a common reason an obviously-correct search returns nothing.
	if got := ftsExpression("invoic", "AND", true); got != `"invoic"*` {
		t.Fatalf("prefix = %q", got)
	}
	if got := ftsExpression("alpha beta", "OR", true); got != `"alpha"* OR "beta"*` {
		t.Fatalf("OR prefix = %q", got)
	}
	if got := ftsExpression("   ", "AND", false); got != "" {
		t.Fatalf("blank query should yield an empty expression, got %q", got)
	}
	// A quote in the query must not break out of the FTS string literal.
	if got := ftsExpression(`say"hi`, "AND", false); !strings.Contains(got, `""`) {
		t.Fatalf("embedded quote not escaped: %q", got)
	}
}

func TestSortHitsPromotesSubjectMatches(t *testing.T) {
	t.Parallel()

	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	hits := []MailSummary{
		{Subject: "Weekly newsletter", From: "news@example.com", When: newer, Snippet: "mentions parcel badge in passing"},
		{Subject: "Parcel badge issue", From: "support@example.com", When: older},
	}
	sortHits(hits, "parcel badge")
	// Pure date ordering would put the newsletter first and bury the answer.
	if hits[0].Subject != "Parcel badge issue" {
		t.Fatalf("subject match not promoted above a newer body-only hit: %+v", hits[0])
	}
}

func TestSortHitsFallsBackToRecency(t *testing.T) {
	t.Parallel()

	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	hits := []MailSummary{
		{Subject: "old", When: older},
		{Subject: "new", When: newer},
	}
	sortHits(hits, "")
	if hits[0].Subject != "new" {
		t.Fatalf("with no query, newest should come first: %+v", hits)
	}
}

func TestDemoteNoiseKeepsEverything(t *testing.T) {
	t.Parallel()

	hits := []MailSummary{
		{Subject: "junk copy", FolderTag: "spam"},
		{Subject: "real", FolderTag: ""},
		{Subject: "trashed", FolderTag: "trash"},
	}
	got := demoteNoise(hits)
	if len(got) != 3 {
		t.Fatalf("demoteNoise dropped messages: %d of 3", len(got))
	}
	if got[0].Subject != "real" {
		t.Fatalf("real mail should rank above Junk/Trash copies: %+v", got)
	}
}

// The identifier must survive intact, or the follow-up read cannot be issued.
func TestMessageJSONCarriesUntruncatedIdentityAndNextCommand(t *testing.T) {
	t.Parallel()

	longID := "<26NPWXWGRVR_6a61f60782d0a_8f25ebf7d9663b_sprut@zendesk.example>"
	longFolder := "ImapMail/mail.example-1.com/Deep/Nested/Folder Name"
	m := MailSummary{
		MessageID: longID,
		Folder:    longFolder,
		Subject:   strings.Repeat("long subject ", 12),
		From:      "Someone With A Very Long Display Name <someone@example.com>",
		When:      time.Date(2026, 7, 23, 11, 7, 51, 0, time.UTC),
		Date:      "2026-07-23 16:37",
	}
	j := toMessageJSON(m)
	if j.MessageID != longID {
		t.Fatalf("Message-ID altered: %q", j.MessageID)
	}
	if j.Folder != longFolder {
		t.Fatalf("folder truncated: %q", j.Folder)
	}
	if j.Subject != m.Subject {
		t.Fatalf("subject truncated: %q", j.Subject)
	}
	if j.Date != "2026-07-23T11:07:51Z" {
		t.Fatalf("date not ISO-8601: %q", j.Date)
	}
	if !strings.Contains(j.Read, longID) {
		t.Fatalf("read command does not carry the full id: %q", j.Read)
	}
}

func TestWantJSONHonoursEnv(t *testing.T) {
	t.Setenv("TB_JSON", "")
	if wantJSON(false) {
		t.Fatal("JSON should be off by default")
	}
	if !wantJSON(true) {
		t.Fatal("explicit --json must win")
	}
	t.Setenv("TB_JSON", "1")
	if !wantJSON(false) {
		t.Fatal("TB_JSON=1 should enable JSON")
	}
	t.Setenv("TB_JSON", "0")
	if wantJSON(false) {
		t.Fatal("TB_JSON=0 should disable JSON")
	}
}
