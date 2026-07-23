package main

import (
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDedupeByMessageIDCollapsesCrossAccountCopies(t *testing.T) {
	t.Parallel()

	hits := []MailSummary{
		{MessageID: "<a@x>", Account: "one@example.com", Folder: "ImapMail/a/INBOX", Subject: "Shared"},
		{MessageID: "<a@x>", Account: "two@example.com", Folder: "ImapMail/b/INBOX", Subject: "Shared"},
		{MessageID: "<b@x>", Account: "one@example.com", Folder: "ImapMail/a/INBOX", Subject: "Other"},
	}
	got := dedupeByMessageID(hits)
	if len(got) != 2 {
		t.Fatalf("expected 2 unique messages, got %d", len(got))
	}
	if len(got[0].AlsoIn) != 1 || !strings.Contains(got[0].AlsoIn[0], "two@example.com") {
		t.Fatalf("duplicate location not recorded: %+v", got[0].AlsoIn)
	}
	// The first (highest-ranked) copy must be the one kept.
	if got[0].Account != "one@example.com" {
		t.Fatalf("kept the wrong copy: %s", got[0].Account)
	}
}

func TestDedupeKeepsMessagesWithoutIDs(t *testing.T) {
	t.Parallel()

	// An empty Message-ID is not an identity; those must never be merged.
	got := dedupeByMessageID([]MailSummary{
		{Subject: "one"}, {Subject: "two"},
	})
	if len(got) != 2 {
		t.Fatalf("messages lacking a Message-ID were merged: %d", len(got))
	}
}

const multipartMessage = "MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=\"BOUND\"\r\n" +
	"\r\n" +
	"--BOUND\r\n" +
	"Content-Type: text/plain; charset=utf-8\r\n" +
	"\r\n" +
	"the body\r\n" +
	"--BOUND\r\n" +
	"Content-Type: application/pdf; name=\"bill.pdf\"\r\n" +
	"Content-Disposition: attachment; filename=\"bill.pdf\"\r\n" +
	"\r\n" +
	"PDFDATA\r\n" +
	"--BOUND\r\n" +
	"Content-Type: image/png\r\n" +
	"Content-Disposition: inline; filename=\"logo.png\"\r\n" +
	"\r\n" +
	"PNGDATA\r\n" +
	"--BOUND--\r\n"

func parseTestMessage(t *testing.T, raw string) (mail.Header, []byte) {
	t.Helper()
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 0, len(raw))
	buf := make([]byte, 1024)
	for {
		n, err := msg.Body.Read(buf)
		body = append(body, buf[:n]...)
		if err != nil {
			break
		}
	}
	return msg.Header, body
}

func TestListAttachmentsSkipsInlineBodyParts(t *testing.T) {
	t.Parallel()

	header, body := parseTestMessage(t, multipartMessage)
	got := listAttachments(header, body)
	if len(got) != 1 {
		t.Fatalf("expected exactly the one real attachment, got %d: %+v", len(got), got)
	}
	if got[0].Filename != "bill.pdf" {
		t.Fatalf("wrong attachment: %+v", got[0])
	}
	if got[0].ContentType != "application/pdf" {
		t.Fatalf("content type = %q", got[0].ContentType)
	}
}

func TestSaveAttachmentsWritesFiles(t *testing.T) {
	t.Parallel()

	header, body := parseTestMessage(t, multipartMessage)
	dir := t.TempDir()
	written, err := saveAttachments(header, body, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 1 {
		t.Fatalf("wrote %d files, want 1: %v", len(written), written)
	}
	data, err := os.ReadFile(written[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "PDFDATA") {
		t.Fatalf("attachment content not written: %q", data)
	}
	if filepath.Base(written[0]) != "bill.pdf" {
		t.Fatalf("unexpected name: %s", written[0])
	}
}

// Filenames arrive from untrusted mail; a traversal must never escape the dir.
func TestSafeAttachmentNameRejectsTraversal(t *testing.T) {
	t.Parallel()

	for _, in := range []string{"../../etc/passwd", "/etc/passwd", `..\..\windows\system32\x`, "..", ".", ""} {
		got := safeAttachmentName(in)
		if strings.Contains(got, "/") || strings.Contains(got, `\`) || got == ".." || got == "." {
			t.Fatalf("safeAttachmentName(%q) = %q, which escapes the directory", in, got)
		}
	}
	if got := safeAttachmentName("bill.pdf"); got != "bill.pdf" {
		t.Fatalf("ordinary name mangled: %q", got)
	}
}

func TestUniquePathAvoidsClobber(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := filepath.Join(dir, "bill.pdf")
	if err := os.WriteFile(first, []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	second := uniquePath(first)
	if second == first {
		t.Fatal("uniquePath returned an existing path; the first file would be overwritten")
	}
	if !strings.Contains(filepath.Base(second), "bill") {
		t.Fatalf("unexpected unique name: %s", second)
	}
}

func TestIsOwnAddress(t *testing.T) {
	t.Parallel()

	accounts := map[string]bool{"ops@example.com": true}
	if !isOwnAddress("Ops Desk <ops@example.com>", accounts) {
		t.Fatal("own address not recognised")
	}
	if isOwnAddress("Someone <other@example.org>", accounts) {
		t.Fatal("foreign address treated as own")
	}
}

func TestReplyRecipientPrefersReplyTo(t *testing.T) {
	t.Parallel()

	got := replyRecipient(MailSummary{
		From:    "Support <noreply@example.com>",
		ReplyTo: "Support Desk <support@example.com>",
	})
	if got != "support@example.com" {
		t.Fatalf("replyRecipient = %q, want the Reply-To address", got)
	}
	got = replyRecipient(MailSummary{From: "Support <support@example.com>"})
	if got != "support@example.com" {
		t.Fatalf("replyRecipient = %q", got)
	}
}
