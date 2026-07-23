package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProfileSelectArgsPrefersAbsolutePath(t *testing.T) {
	t.Parallel()

	// `-P <name>` falls back to the graphical profile manager when the name
	// does not bind. On a headless host that means the client waits forever on
	// an invisible "Choose User Profile" dialog while tb reports success.
	got := profileSelectArgs(Profile{Name: "base_config", AbsolutePath: "/home/user/.thunderbird/xyz.default-release"})
	want := []string{"-profile", "/home/user/.thunderbird/xyz.default-release"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("profileSelectArgs() = %v, want %v", got, want)
	}
}

func TestProfileSelectArgsFallsBackToName(t *testing.T) {
	t.Parallel()

	got := profileSelectArgs(Profile{Name: "base_config"})
	if len(got) != 2 || got[0] != "-P" || got[1] != "base_config" {
		t.Fatalf("profileSelectArgs() = %v, want [-P base_config]", got)
	}
}

func TestMailTreeFingerprintDetectsNewMail(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	inbox := filepath.Join(dir, "ImapMail", "mail.example.com")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(inbox, "INBOX")
	if err := os.WriteFile(path, []byte("From a@b\r\nSubject: one\r\n\r\nbody\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	profile := Profile{AbsolutePath: dir}
	before := mailTreeFingerprint(profile)
	if before == "" {
		t.Fatal("fingerprint of a populated profile should not be empty")
	}
	if again := mailTreeFingerprint(profile); again != before {
		t.Fatal("fingerprint is not stable across calls on an unchanged tree")
	}

	// Appending mail must change the fingerprint — this is what lets a sync
	// timeout be judged on evidence instead of assumed to have worked.
	time.Sleep(10 * time.Millisecond)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("From c@d\r\nSubject: two\r\n\r\nbody\r\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if after := mailTreeFingerprint(profile); after == before {
		t.Fatal("fingerprint did not change after new mail was appended")
	}
}

func TestMailTreeFingerprintEmptyForUnknownProfile(t *testing.T) {
	t.Parallel()

	if got := mailTreeFingerprint(Profile{}); got != "" {
		t.Fatalf("fingerprint of a profile with no path = %q, want empty", got)
	}
}
