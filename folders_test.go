package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"strings"
	"testing"

	"github.com/emersion/go-mbox"
)

// captureLog collects everything written to the standard logger during fn.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	prevOut := log.Writer()
	prevFlags := log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	}()
	fn()
	return buf.String()
}

func TestSkipTrackerCollapsesRepeatedFormatWarnings(t *testing.T) {
	// A single non-mbox file (Yahoo's Trash) produced one warning per record —
	// over twelve thousand lines for one folder.
	out := captureLog(t, func() {
		var skips skipTracker
		for i := 0; i < 5000; i++ {
			skips.note("show", "ImapMail/imap.mail.yahoo.com/Trash", mbox.ErrInvalidFormat)
		}
		skips.report()
	})
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("expected exactly one summary line, got:\n%s", out)
	}
	if !strings.Contains(out, "skipped 1 folder(s)") {
		t.Fatalf("summary should count folders, not records: %q", out)
	}
}

func TestSkipTrackerStillReportsRealErrorsOncePerFolder(t *testing.T) {
	out := captureLog(t, func() {
		var skips skipTracker
		for i := 0; i < 3; i++ {
			skips.note("ingest", "Mail/Local Folders/Archive", errors.New("permission denied"))
		}
		skips.report()
	})
	if strings.Count(out, "permission denied") != 1 {
		t.Fatalf("real error should be reported exactly once per folder, got:\n%s", out)
	}
}

func TestSkipTrackerVerboseListsEachFolderOnce(t *testing.T) {
	t.Setenv("TB_VERBOSE", "1")
	out := captureLog(t, func() {
		var skips skipTracker
		for i := 0; i < 100; i++ {
			skips.note("show", fmt.Sprintf("Mail/box%d", i%2), mbox.ErrInvalidFormat)
		}
		skips.report()
	})
	if got := strings.Count(out, "invalid mbox format"); got != 2 {
		t.Fatalf("expected one verbose line per folder (2), got %d:\n%s", got, out)
	}
}

func testMailboxes() []Mailbox {
	names := []string{
		"ImapMail/imap.gmail.com/INBOX",
		"ImapMail/mail.gour-1.top/INBOX",
		"ImapMail/mail.gour-1.top/Sent Items",
		"ImapMail/mail.gour-1.top/Junk Mail",
		"ImapMail/imap.mail.yahoo.com/Trash",
	}
	boxes := make([]Mailbox, 0, len(names))
	for _, name := range names {
		boxes = append(boxes, Mailbox{Name: name, Path: "/profile/" + name})
	}
	return boxes
}

func TestFilterMailboxesFuzzyFallback(t *testing.T) {
	t.Parallel()

	// "gour sent" matches no folder as a substring, but every token appears in
	// ImapMail/mail.gour-1.top/Sent Items.
	got, err := filterMailboxes(testMailboxes(), "gour sent", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "ImapMail/mail.gour-1.top/Sent Items" {
		t.Fatalf("fuzzy match = %v, want the gour Sent Items folder", got)
	}
}

func TestFilterMailboxesPrefersSubstringOverFuzzy(t *testing.T) {
	t.Parallel()

	got, err := filterMailboxes(testMailboxes(), "Sent Items", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "ImapMail/mail.gour-1.top/Sent Items" {
		t.Fatalf("substring match = %v", got)
	}
}

func TestFilterMailboxesUnknownFolderListsCandidates(t *testing.T) {
	t.Parallel()

	_, err := filterMailboxes(testMailboxes(), "NoSuchFolder", "", nil)
	if err == nil {
		t.Fatal("expected an error for an unknown folder")
	}
	// The operator must see what does exist; a bare "no folders match" leaves
	// them guessing whether the mail is absent or the name is wrong.
	if !strings.Contains(err.Error(), "available:") || !strings.Contains(err.Error(), "INBOX") {
		t.Fatalf("error does not list candidates: %v", err)
	}
}

func TestInboxMailboxes(t *testing.T) {
	t.Parallel()

	got := inboxMailboxes(testMailboxes())
	if len(got) != 2 {
		t.Fatalf("inboxMailboxes() returned %d folders, want 2: %v", len(got), got)
	}
	for _, box := range got {
		if !strings.HasSuffix(box.Name, "/INBOX") {
			t.Fatalf("non-inbox folder selected: %s", box.Name)
		}
	}
}

func TestDescribeFoldersTruncates(t *testing.T) {
	t.Parallel()

	got := describeFolders(testMailboxes(), 2)
	if !strings.Contains(got, "(+3 more)") {
		t.Fatalf("describeFolders() did not report the remainder: %q", got)
	}
	if describeFolders(nil, 5) != "(none)" {
		t.Fatal("describeFolders(nil) should read (none)")
	}
}
