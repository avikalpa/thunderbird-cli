package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFilterMailboxesByAccountAndFolder(t *testing.T) {
	boxes := []Mailbox{
		{Name: "ImapMail/mail.example/INBOX", Path: "/p/ImapMail/mail.example/INBOX"},
		{Name: "ImapMail/mail.example/Junk Mail", Path: "/p/ImapMail/mail.example/Junk Mail"},
		{Name: "ImapMail/other.example/INBOX", Path: "/p/ImapMail/other.example/INBOX"},
	}
	dirToAccount := map[string]string{
		"/p/ImapMail/mail.example":  "ops@example.test",
		"/p/ImapMail/other.example": "other@example.test",
	}

	filtered, err := filterMailboxes(boxes, "INBOX", "ops@example.test", dirToAccount)
	if err != nil {
		t.Fatalf("filterMailboxes: %v", err)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 mailbox, got %d", len(filtered))
	}
	if filtered[0].Path != "/p/ImapMail/mail.example/INBOX" {
		t.Fatalf("unexpected mailbox path: %s", filtered[0].Path)
	}
}

func TestMatchMessageByMessageID(t *testing.T) {
	msg := MailSummary{
		Subject:   "Re: account activation",
		From:      "Person <person@example.test>",
		MessageID: "<abc123@example.test>",
	}

	if !matchMessage(msg, "body text", "", "<abc123@example.test>") {
		t.Fatalf("expected exact message-id match")
	}
	if matchMessage(msg, "body text", "", "<different@example.test>") {
		t.Fatalf("did not expect mismatched message-id to match")
	}
}

func TestSearchFallsBackToDirectMailboxScanWhenCacheIsEmpty(t *testing.T) {
	t.Setenv("TB_SQLITE_PATH", filepath.Join(t.TempDir(), "index.db"))

	profileRoot := t.TempDir()
	profilePath := filepath.Join(profileRoot, "cold-profile")
	if err := os.MkdirAll(filepath.Join(profilePath, "ImapMail", "mail.example"), 0o755); err != nil {
		t.Fatalf("mkdir profile: %v", err)
	}
	profilesINI := `
[Profile0]
Name=cold-profile
IsRelative=1
Path=cold-profile
Default=1
`
	if err := os.WriteFile(filepath.Join(profileRoot, "profiles.ini"), []byte(strings.TrimSpace(profilesINI)), 0o644); err != nil {
		t.Fatalf("write profiles.ini: %v", err)
	}
	prefs := `
user_pref("mail.accountmanager.accounts", "account1");
user_pref("mail.account.account1.server", "server1");
user_pref("mail.account.account1.identities", "id1");
user_pref("mail.server.server1.directory-rel", "[ProfD]ImapMail/mail.example");
user_pref("mail.identity.id1.useremail", "ops@example.test");
`
	if err := os.WriteFile(filepath.Join(profilePath, "prefs.js"), []byte(strings.TrimSpace(prefs)), 0o644); err != nil {
		t.Fatalf("write prefs: %v", err)
	}
	mbox := `From sender@example.test Tue Dec 23 00:00:00 2025
From: Twitch <no-reply@twitch.tv>
To: ops@example.test
Subject: Your Twitch account avikalpa has been permanently suspended
Date: Tue, 23 Dec 2025 00:00:00 +0000
Message-ID: <m1@example.test>
Content-Type: text/plain; charset=UTF-8

Hello avikalpa, your Twitch account is permanently suspended for fraud.
`
	if err := os.WriteFile(filepath.Join(profilePath, "ImapMail", "mail.example", "INBOX"), []byte(mbox), 0o644); err != nil {
		t.Fatalf("write mailbox: %v", err)
	}

	app := &App{Root: profileRoot}
	out := captureStdout(t, func() {
		if err := app.search("twitch suspended", "cold-profile", "", "ops@example.test", 10, true, time.Time{}, time.Time{}, false, false, false); err != nil {
			t.Fatalf("search: %v", err)
		}
	})
	if !strings.Contains(out, "Your Twitch account avikalpa has been permanently suspended") {
		t.Fatalf("expected direct fallback hit, got %q", out)
	}

	store, err := openSQLiteStore(os.Getenv("TB_SQLITE_PATH"))
	if err != nil {
		t.Fatalf("openSQLiteStore(): %v", err)
	}
	defer store.Close()
	count, err := store.CountMessages(context.Background(), "cold-profile")
	if err != nil {
		t.Fatalf("CountMessages(): %v", err)
	}
	if count != 0 {
		t.Fatalf("expected empty cache after direct fallback search, got %d indexed message(s)", count)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe(): %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close write pipe: %v", err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	return string(out)
}
