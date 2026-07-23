package main

import (
	"testing"
	"time"
)

func refNow() time.Time {
	return time.Date(2026, 7, 23, 15, 30, 0, 0, time.UTC)
}

func TestParseTimeSpecForms(t *testing.T) {
	t.Parallel()
	now := refNow()

	tests := []struct {
		spec string
		want time.Time
	}{
		{"", time.Time{}},
		{"today", time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)},
		{"yesterday", time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)},
		{"2019-07-05", time.Date(2019, 7, 5, 0, 0, 0, 0, time.UTC)},
		// A bare month is how an old bill is actually remembered.
		{"2019-07", time.Date(2019, 7, 1, 0, 0, 0, 0, time.UTC)},
		{"24h", now.Add(-24 * time.Hour)},
		{"7d", now.Add(-7 * 24 * time.Hour)},
		{"2w", now.Add(-14 * 24 * time.Hour)},
	}
	for _, tt := range tests {
		got, err := parseTimeSpec(tt.spec, now)
		if err != nil {
			t.Fatalf("parseTimeSpec(%q) error: %v", tt.spec, err)
		}
		if !got.Equal(tt.want) {
			t.Fatalf("parseTimeSpec(%q) = %v, want %v", tt.spec, got, tt.want)
		}
	}
	if _, err := parseTimeSpec("last tuesday", now); err == nil {
		t.Fatal("expected an error for an unparseable spec")
	}
}

// --till must be inclusive of the named day/month, or "the July 2019 bill"
// silently excludes most of July.
func TestEndOfTimeSpecIsInclusive(t *testing.T) {
	t.Parallel()
	now := refNow()

	got, err := endOfTimeSpec("2019-07", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2019, 8, 1, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("endOfTimeSpec(2019-07) = %v, want %v (exclusive upper bound)", got, want)
	}

	got, err = endOfTimeSpec("2019-07-05", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2019, 7, 6, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("endOfTimeSpec(2019-07-05) = %v, want %v", got, want)
	}

	got, err = endOfTimeSpec("today", now)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("endOfTimeSpec(today) = %v, want %v", got, want)
	}
}

func TestScoreImportanceSeparatesBulkFromPersonal(t *testing.T) {
	t.Parallel()
	accounts := map[string]bool{"ops@example.com": true}

	personal := scoreImportance(MailSummary{
		From:       "A Person <someone@example.org>",
		Subject:    "Re: contract question",
		Recipients: "ops@example.com",
		InReplyTo:  "<parent@example.org>",
	}, accounts)

	bulk := scoreImportance(MailSummary{
		From:       "News <no-reply@marketing.example>",
		Subject:    "This week in widgets",
		Recipients: "ops@example.com",
		ListID:     "<weekly.marketing.example>",
	}, accounts)

	if !(personal.Score > bulk.Score) {
		t.Fatalf("personal (%d) should outrank bulk (%d)", personal.Score, bulk.Score)
	}
	if !bulk.Bulk {
		t.Fatal("list headers should mark a message as bulk")
	}
	if personal.Bulk {
		t.Fatal("a direct reply should not be marked bulk")
	}
	if len(personal.Reasons) == 0 {
		t.Fatal("scoring must explain itself; reasons were empty")
	}
}

func TestScoreImportanceIgnoresInReplyToOnBulkMail(t *testing.T) {
	t.Parallel()

	// Campaign mailers set In-Reply-To on follow-ups; that alone must not make
	// a newsletter look like a conversation the user is part of.
	v := scoreImportance(MailSummary{
		From:      "Campaign <noreply@shop.example>",
		Subject:   "Your weekly digest",
		InReplyTo: "<campaign-123@shop.example>",
		ListID:    "<digest.shop.example>",
	}, nil)
	for _, r := range v.Reasons {
		if r == "reply in an existing thread" {
			t.Fatalf("bulk mail credited as a thread reply: %+v", v.Reasons)
		}
	}
}

func TestScoreImportanceDemotesJunk(t *testing.T) {
	t.Parallel()

	junk := scoreImportance(MailSummary{Subject: "hello", FolderTag: "spam"}, nil)
	plain := scoreImportance(MailSummary{Subject: "hello"}, nil)
	if junk.Score >= plain.Score {
		t.Fatalf("junk (%d) should score below normal mail (%d)", junk.Score, plain.Score)
	}
}
