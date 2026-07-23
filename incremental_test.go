package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// mboxMessage renders one syntactically valid mbox entry.
func mboxMessage(id, subject string) string {
	return fmt.Sprintf("From sender@example.com Thu Jul 23 10:00:00 2026\r\n"+
		"From: Sender <sender@example.com>\r\n"+
		"To: ops@example.org\r\n"+
		"Subject: %s\r\n"+
		"Message-ID: <%s@example.com>\r\n"+
		"Date: Thu, 23 Jul 2026 10:00:00 +0000\r\n"+
		"\r\n"+
		"body of %s\r\n\r\n", subject, id, subject)
}

func writeMbox(t *testing.T, path string, msgs ...string) {
	t.Helper()
	var buf string
	for _, m := range msgs {
		buf += m
	}
	if err := os.WriteFile(path, []byte(buf), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFingerprintRoundTrip(t *testing.T) {
	t.Parallel()

	fp := folderFingerprint(time.Unix(1700000000, 0), 4096)
	size, ok := fingerprintIngestedSize(fp)
	if !ok || size != 4096 {
		t.Fatalf("fingerprintIngestedSize(%q) = %d, %v", fp, size, ok)
	}
	// A v1 fingerprint from an older tb must not be mistaken for a byte offset.
	if _, ok := fingerprintIngestedSize("1700000000:4096"); ok {
		t.Fatal("legacy v1 fingerprint was parsed as a v2 size")
	}
	if _, ok := fingerprintIngestedSize(""); ok {
		t.Fatal("empty fingerprint was parsed as a size")
	}
}

func TestMboxAppendOffsetAcceptsAppend(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "INBOX")
	first := mboxMessage("one", "First")
	writeMbox(t, path, first)
	prevSize := int64(len(first))

	writeMbox(t, path, first, mboxMessage("two", "Second"))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	offset, ok := mboxAppendOffset(path, prevSize, info.Size())
	if !ok || offset != prevSize {
		t.Fatalf("mboxAppendOffset() = %d, %v; want %d, true", offset, ok, prevSize)
	}
}

func TestMboxAppendOffsetRejectsRewrite(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "INBOX")
	first := mboxMessage("one", "First")
	prevSize := int64(len(first))

	// Simulate compaction: the old message is gone and different mail occupies
	// the file. The previous end no longer lands on a message separator, so
	// resuming there would silently skip mail.
	writeMbox(t, path, mboxMessage("other", "Completely different message here"))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mboxAppendOffset(path, prevSize, info.Size()); ok {
		t.Fatal("mboxAppendOffset accepted a rewritten mbox")
	}
}

func TestMboxAppendOffsetRejectsShrinkAndNoGrowth(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "INBOX")
	writeMbox(t, path, mboxMessage("one", "First"))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := mboxAppendOffset(path, info.Size(), info.Size()); ok {
		t.Fatal("accepted an unchanged size")
	}
	if _, ok := mboxAppendOffset(path, info.Size()+100, info.Size()); ok {
		t.Fatal("accepted a shrunken file")
	}
	if _, ok := mboxAppendOffset(path, 0, info.Size()); ok {
		t.Fatal("accepted a zero previous size (nothing ingested yet)")
	}
}

func TestScanMailboxFromReadsOnlyNewMessages(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "INBOX")
	first := mboxMessage("one", "First")
	writeMbox(t, path, first, mboxMessage("two", "Second"), mboxMessage("three", "Third"))

	box := Mailbox{Name: "INBOX", Path: path}
	msgs, err := scanMailboxFrom(box, int64(len(first)), "ops@example.org", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("scanned %d messages, want 2 (the appended ones): %+v", len(msgs), msgs)
	}
	for _, m := range msgs {
		if m.Subject == "First" {
			t.Fatal("re-read a message that was already ingested")
		}
	}
	if msgs[0].Subject != "Second" || msgs[1].Subject != "Third" {
		t.Fatalf("unexpected subjects: %q, %q", msgs[0].Subject, msgs[1].Subject)
	}
}

// The whole point of the optimisation: a full scan and a resumed scan must
// agree on the appended messages.
func TestIncrementalMatchesFullScanForAppendedMail(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "INBOX")
	existing := mboxMessage("a", "Alpha") + mboxMessage("b", "Bravo")
	appended := mboxMessage("c", "Charlie") + mboxMessage("d", "Delta")
	writeMbox(t, path, existing, appended)

	box := Mailbox{Name: "INBOX", Path: path}
	full, err := searchMailbox(box, func(string) bool { return true }, 0, time.Time{}, time.Time{}, 0, "ops@example.org", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 4 {
		t.Fatalf("full scan found %d messages, want 4", len(full))
	}

	incremental, err := scanMailboxFrom(box, int64(len(existing)), "ops@example.org", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(incremental) != 2 {
		t.Fatalf("incremental scan found %d messages, want 2", len(incremental))
	}
	for i, m := range incremental {
		want := full[2+i]
		if m.MessageID != want.MessageID || m.Subject != want.Subject {
			t.Fatalf("incremental[%d] = (%s, %s), full = (%s, %s)", i, m.MessageID, m.Subject, want.MessageID, want.Subject)
		}
	}
}
