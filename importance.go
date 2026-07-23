package main

import (
	"strings"
)

// Importance.
//
// "What's important today?" is a different question from "what matches these
// words". Most of a day's mail is bulk: newsletters, receipts, alerts. The
// signals that separate mail written *to a person* from mail blasted at a list
// are all in the headers, so they are captured at ingest and scored here.
//
// This is a heuristic and is reported as a score with reasons, never as a
// verdict — the caller can see why something ranked where it did.

type importanceVerdict struct {
	Score   int
	Reasons []string
	Bulk    bool
}

var bulkSenderMarkers = []string{
	"no-reply", "noreply", "no_reply", "donotreply", "do-not-reply",
	"notifications@", "notification@", "mailer@", "bounce", "newsletter",
	"marketing@", "updates@", "alerts@", "info@", "support@no",
}

// scoreImportance rates one message. accounts are the user's own addresses,
// used to tell "addressed to me" from "I am one of a thousand recipients".
func scoreImportance(m MailSummary, accounts map[string]bool) importanceVerdict {
	v := importanceVerdict{}
	from := strings.ToLower(m.From)
	subject := strings.ToLower(m.Subject)

	// Bulk markers: a List-Id/List-Unsubscribe header is the strongest single
	// signal that a human did not write this to this person.
	if strings.TrimSpace(m.ListID) != "" {
		v.Score -= 4
		v.Bulk = true
		v.Reasons = append(v.Reasons, "bulk: carries list/unsubscribe headers")
	}
	for _, marker := range bulkSenderMarkers {
		if strings.Contains(from, marker) {
			v.Score -= 2
			v.Bulk = true
			v.Reasons = append(v.Reasons, "bulk: automated sender address")
			break
		}
	}

	// Addressed directly: the user's address in To/Cc, and few other recipients.
	recipients := strings.ToLower(m.Recipients)
	if recipients != "" {
		addressed := false
		for acct := range accounts {
			if acct != "" && strings.Contains(recipients, acct) {
				addressed = true
				break
			}
		}
		if addressed {
			v.Score += 3
			v.Reasons = append(v.Reasons, "addressed directly to you")
		}
		if n := strings.Count(recipients, "@"); n > 8 {
			v.Score -= 2
			v.Reasons = append(v.Reasons, "mass recipient list")
		}
	}

	// Part of a conversation: someone replied, which implies a thread the user
	// is already in. Bulk senders also set In-Reply-To on campaign follow-ups,
	// so that header alone does not count — the subject must look like a reply,
	// or the sender must not already be marked bulk.
	looksLikeReply := strings.HasPrefix(subject, "re:") || strings.HasPrefix(subject, "fwd:")
	if looksLikeReply || (strings.TrimSpace(m.InReplyTo) != "" && !v.Bulk) {
		v.Score += 3
		v.Reasons = append(v.Reasons, "reply in an existing thread")
	}

	// Words that usually mean a deadline or money.
	for _, kw := range []string{"invoice", "payment", "due", "overdue", "urgent",
		"action required", "deadline", "expire", "suspend", "verify", "receipt",
		"refund", "order", "delivery", "appointment", "interview", "offer"} {
		if strings.Contains(subject, kw) {
			v.Score += 2
			v.Reasons = append(v.Reasons, "subject mentions "+kw)
			break
		}
	}

	switch m.FolderTag {
	case "spam":
		v.Score -= 5
		v.Bulk = true
		v.Reasons = append(v.Reasons, "in a junk folder")
	case "trash":
		v.Score -= 5
		v.Reasons = append(v.Reasons, "in a trash folder")
	}

	return v
}

// profileAccounts returns the set of addresses this profile owns, lowercased.
func (a *App) profileAccounts(profile Profile) map[string]bool {
	out := map[string]bool{}
	prefs, err := parsePrefs(prefsPath(profile))
	if err != nil {
		return out
	}
	for key, value := range prefs {
		if strings.HasPrefix(key, "mail.identity.") && strings.HasSuffix(key, ".useremail") {
			if v := strings.ToLower(strings.TrimSpace(value)); v != "" {
				out[v] = true
			}
		}
	}
	return out
}
