package main

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStoreRoundTrip(t *testing.T) {
	t.Parallel()

	store, err := openSQLiteStore(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatalf("openSQLiteStore(): %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	now := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	msgs := []MailSummary{
		{
			Profile:   "default",
			MessageID: "<m1@example.test>",
			Folder:    "INBOX",
			Subject:   "Ghostty packaging update",
			From:      "ops@example.test",
			Snippet:   "SQLite default backend landed",
			Search:    "Ghostty packaging update SQLite default backend landed",
			When:      now,
			Date:      now.Format(time.RFC3339),
			Account:   "ops@example.test",
		},
		{
			Profile:   "default",
			MessageID: "<m2@example.test>",
			Folder:    "Sent",
			Subject:   "Release plan",
			From:      "ops@example.test",
			Snippet:   "curl installer and tb update",
			Search:    "Release plan curl installer and tb update",
			When:      now.Add(-time.Hour),
			Date:      now.Add(-time.Hour).Format(time.RFC3339),
			Account:   "ops@example.test",
		},
	}
	if err := store.Upsert(ctx, msgs); err != nil {
		t.Fatalf("Upsert(): %v", err)
	}

	results, err := store.Search(ctx, queryOptions{
		query:   "SQLite backend",
		profile: "default",
		limit:   10,
	})
	if err != nil {
		t.Fatalf("Search(): %v", err)
	}
	if len(results) != 1 || results[0].MessageID != "<m1@example.test>" {
		t.Fatalf("Search() returned %#v", results)
	}

	if err := store.SetMeta(ctx, "last_scan", "2026-03-25T12:00:00Z"); err != nil {
		t.Fatalf("SetMeta(): %v", err)
	}
	val, err := store.GetMeta(ctx, "last_scan")
	if err != nil {
		t.Fatalf("GetMeta(): %v", err)
	}
	if val != "2026-03-25T12:00:00Z" {
		t.Fatalf("GetMeta() = %q", val)
	}

	if err := store.PruneMissing(ctx, "default", []string{"<m1@example.test>"}); err != nil {
		t.Fatalf("PruneMissing(): %v", err)
	}
	count, err := store.CountMessages(ctx, "default")
	if err != nil {
		t.Fatalf("CountMessages(): %v", err)
	}
	if count != 1 {
		t.Fatalf("CountMessages() = %d, want 1", count)
	}
}
