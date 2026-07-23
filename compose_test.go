package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testSendAccount() sendAccountConfig {
	return sendAccountConfig{
		Identity: identityConfig{
			Email:      "ops@example.org",
			FullName:   "Ops Desk",
			SentFolder: "imap://ops%40example.org@mail.example.org/Sent%20Items",
		},
		Outgoing: serverConfig{Hostname: "smtp.example.org", Port: 465},
		Incoming: serverConfig{Hostname: "imap.example.org", Port: 993},
	}
}

func TestBuildOutgoingMessageSetsThreadingHeaders(t *testing.T) {
	t.Parallel()

	raw, recipients, messageID, err := buildOutgoingMessage(testSendAccount(), composeRequest{
		To:        "support@example.com",
		Subject:   "Re: Ticket 13421571",
		Body:      "Following up.",
		InReplyTo: "parent@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	msg := string(raw)
	if !strings.Contains(msg, "In-Reply-To: <parent@example.com>\r\n") {
		t.Fatalf("In-Reply-To header missing or unbracketed:\n%s", msg)
	}
	// With no explicit --references, the parent Message-ID is the correct
	// minimal chain and is what mail clients thread on.
	if !strings.Contains(msg, "References: <parent@example.com>\r\n") {
		t.Fatalf("References not derived from In-Reply-To:\n%s", msg)
	}
	if messageID == "" || !strings.Contains(msg, "Message-ID: "+messageID) {
		t.Fatalf("returned Message-ID %q not present in the message", messageID)
	}
	if len(recipients) != 1 || recipients[0] != "support@example.com" {
		t.Fatalf("recipients = %v", recipients)
	}
}

func TestBuildOutgoingMessageKeepsExplicitReferences(t *testing.T) {
	t.Parallel()

	raw, _, _, err := buildOutgoingMessage(testSendAccount(), composeRequest{
		To:         "support@example.com",
		Body:       "Body.",
		InReplyTo:  "<second@example.com>",
		References: "<first@example.com>, second@example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "References: <first@example.com> <second@example.com>\r\n") {
		t.Fatalf("explicit References not preserved in order:\n%s", raw)
	}
}

func TestBuildOutgoingMessageOmitsThreadingHeadersWhenUnset(t *testing.T) {
	t.Parallel()

	raw, _, _, err := buildOutgoingMessage(testSendAccount(), composeRequest{
		To:   "support@example.com",
		Body: "Body.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "In-Reply-To:") || strings.Contains(string(raw), "References:") {
		t.Fatalf("threading headers written for a non-reply:\n%s", raw)
	}
}

func TestNormalizeMessageIDList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"a@b", "<a@b>"},
		{"<a@b>", "<a@b>"},
		{"<a@b> <c@d>", "<a@b> <c@d>"},
		{"a@b, c@d", "<a@b> <c@d>"},
		{"  <a@b> ,c@d ", "<a@b> <c@d>"},
	}
	for _, tt := range tests {
		if got := normalizeMessageIDList(tt.in); got != tt.want {
			t.Fatalf("normalizeMessageIDList(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestComposeRequestThreaded(t *testing.T) {
	t.Parallel()

	if (composeRequest{}).threaded() {
		t.Fatal("empty request reported as threaded")
	}
	if !(composeRequest{InReplyTo: "a@b"}).threaded() {
		t.Fatal("--in-reply-to not reported as threaded")
	}
	if !(composeRequest{References: "a@b"}).threaded() {
		t.Fatal("--references not reported as threaded")
	}
}

func TestReadBodyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "body.txt")
	want := "line one\nline two\n"
	if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := readBodyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("readBodyFile() = %q, want %q", got, want)
	}
	if _, err := readBodyFile(filepath.Join(dir, "missing.txt")); err == nil {
		t.Fatal("readBodyFile() on a missing path returned no error")
	}
}
