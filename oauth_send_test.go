package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSentMailboxName(t *testing.T) {
	t.Parallel()

	got := sentMailboxName("imap://avikalpakundu%40gmail.com@imap.gmail.com/[Gmail]/Sent%20Mail")
	if got != "[Gmail]/Sent Mail" {
		t.Fatalf("sentMailboxName() = %q, want %q", got, "[Gmail]/Sent Mail")
	}

	got = sentMailboxName("imap://avikalpa%40outlook.com@outlook.office365.com/Sent")
	if got != "Sent" {
		t.Fatalf("sentMailboxName() = %q, want %q", got, "Sent")
	}

	got = sentMailboxName("imap://avikalpa%40yahoo.com@imap.mail.yahoo.com/Sent")
	if got != "Sent" {
		t.Fatalf("sentMailboxName() = %q, want %q", got, "Sent")
	}
}

func TestParsePrefsSupportsQuotedAndScalarValues(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "prefs.js")
	err := os.WriteFile(path, []byte(`user_pref("mail.identity.id2.useremail", "avikalpakundu@gmail.com");
user_pref("mail.smtpserver.smtp2.port", 465);
user_pref("mail.smtpserver.smtp2.authMethod", 10);
user_pref("mail.server.server3.login_at_startup", true);
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}

	prefs, err := parsePrefs(path)
	if err != nil {
		t.Fatal(err)
	}
	if prefs["mail.identity.id2.useremail"] != "avikalpakundu@gmail.com" {
		t.Fatalf("quoted pref mismatch: %q", prefs["mail.identity.id2.useremail"])
	}
	if prefs["mail.smtpserver.smtp2.port"] != "465" {
		t.Fatalf("port pref mismatch: %q", prefs["mail.smtpserver.smtp2.port"])
	}
	if prefs["mail.smtpserver.smtp2.authMethod"] != "10" {
		t.Fatalf("authMethod pref mismatch: %q", prefs["mail.smtpserver.smtp2.authMethod"])
	}
	if prefs["mail.server.server3.login_at_startup"] != "true" {
		t.Fatalf("bool pref mismatch: %q", prefs["mail.server.server3.login_at_startup"])
	}
}

func TestDefaultPortByHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host     string
		incoming bool
		want     int
	}{
		{"smtp.gmail.com", false, 465},
		{"smtp.office365.com", false, 587},
		{"smtp.mail.yahoo.com", false, 465},
		{"outlook.office365.com", true, 993},
		{"imap.mail.yahoo.com", true, 993},
	}
	for _, tt := range tests {
		if got := defaultPort(tt.host, tt.incoming); got != tt.want {
			t.Fatalf("defaultPort(%q, %v) = %d, want %d", tt.host, tt.incoming, got, tt.want)
		}
	}
}
