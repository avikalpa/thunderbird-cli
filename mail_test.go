package main

import "testing"

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
