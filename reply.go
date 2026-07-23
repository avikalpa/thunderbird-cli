package main

import (
	"context"
	"fmt"
	"log"
	"net/mail"
	"strings"
	"time"
)

// Replying to a thread in one call.
//
// Replying used to be three steps — search, extract the Message-ID, then hand-
// assemble `compose --in-reply-to --references --from --subject`. Every one of
// those was a chance to get the headers wrong, and getting them wrong opens a
// new support ticket instead of continuing the existing one.
//
// This resolves the target from the same query syntax as `tb q`, derives the
// headers from the message being answered, and **prints what it would send
// unless --send is given**. Sending is outward-facing and irreversible; a
// dry-run default means a wrong target costs nothing.

type replyRequest struct {
	Query     string
	Profile   string
	Account   string
	BodyFile  string
	Body      string
	From      string
	Send      bool
	Verify    time.Duration
	ReplyAll  bool
	MessageID string
}

// replyToThread finds the message being answered and builds the reply.
func (a *App) replyToThread(req replyRequest) error {
	profile, err := a.resolveProfile(req.Profile)
	if err != nil {
		return err
	}

	target, err := a.resolveReplyTarget(profile, req)
	if err != nil {
		return err
	}

	body := req.Body
	if req.BodyFile != "" {
		loaded, err := readBodyFile(req.BodyFile)
		if err != nil {
			return err
		}
		body = loaded
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("a reply body is required (--body-file, or --body for one line)")
	}

	// Reply from the address that received it, so the thread stays on the same
	// participant. A support desk keys the requester off the From address.
	from := strings.TrimSpace(req.From)
	if from == "" {
		from = strings.TrimSpace(target.Account)
	}
	if from == "" {
		return fmt.Errorf("could not determine which identity received %q; pass --from", target.Subject)
	}

	to := replyRecipient(target)
	if to == "" {
		return fmt.Errorf("message %q has no usable From address to reply to", target.MessageID)
	}

	subject := target.Subject
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(subject)), "re:") {
		subject = "Re: " + strings.TrimSpace(subject)
	}

	references := strings.TrimSpace(target.References)
	if references == "" {
		references = strings.TrimSpace(target.InReplyTo)
	}
	if references != "" {
		references = references + " " + target.MessageID
	} else {
		references = target.MessageID
	}

	compose := composeRequest{
		To:         to,
		From:       from,
		Subject:    subject,
		Body:       body,
		InReplyTo:  target.MessageID,
		References: references,
	}

	if !req.Send {
		fmt.Println("DRY RUN — nothing was sent. Re-run with --send to deliver.")
		fmt.Println()
		fmt.Printf("Replying to : %s\n", target.Subject)
		fmt.Printf("  received   : %s  (%s)\n", target.Date, target.Folder)
		fmt.Printf("  message-id : %s\n", target.MessageID)
		fmt.Println()
		fmt.Printf("From        : %s\n", compose.From)
		fmt.Printf("To          : %s\n", compose.To)
		fmt.Printf("Subject     : %s\n", compose.Subject)
		fmt.Printf("In-Reply-To : %s\n", compose.InReplyTo)
		fmt.Printf("References  : %s\n", compose.References)
		fmt.Println()
		fmt.Println(strings.Repeat("-", 72))
		fmt.Println(compose.Body)
		fmt.Println(strings.Repeat("-", 72))
		return nil
	}

	log.Printf("info: replying to %q (%s)", target.Subject, target.MessageID)
	res, err := a.sendHeadlessly(profile, compose)
	if err != nil {
		return err
	}
	reportSend(res)
	return a.verifySend(profile, compose, res, req.Verify)
}

// resolveReplyTarget picks the message to answer: an explicit Message-ID, or
// the newest message of the best-matching thread.
func (a *App) resolveReplyTarget(profile Profile, req replyRequest) (MailSummary, error) {
	if id := strings.TrimSpace(req.MessageID); id != "" {
		found, err := a.findByMessageID(profile, id)
		if err != nil {
			return MailSummary{}, err
		}
		return found, nil
	}
	if strings.TrimSpace(req.Query) == "" {
		return MailSummary{}, fmt.Errorf("give a query or --message-id naming the thread to reply to")
	}

	store, err := openStore()
	if err != nil {
		return MailSummary{}, err
	}
	defer store.Close()

	hits, err := store.Search(context.Background(), queryOptions{
		ftsExpr: ftsExpression(req.Query, "AND", true),
		account: strings.ToLower(strings.TrimSpace(req.Account)),
		profile: profile.Name,
		limit:   0,
	})
	if err != nil {
		return MailSummary{}, err
	}
	hits = demoteNoise(hits)
	sortHits(hits, req.Query)
	if len(hits) == 0 {
		return MailSummary{}, fmt.Errorf("no message matches %q, so there is nothing to reply to", req.Query)
	}

	// Answer the newest message of the winning thread, not the best-scoring one:
	// a reply belongs at the end of the conversation.
	thread, err := a.expandThread(profile, hits[0])
	if err != nil || len(thread) == 0 {
		return hits[0], nil
	}
	newest := thread[0]
	for _, m := range thread {
		if m.When.After(newest.When) {
			newest = m
		}
	}
	// Never reply to your own last word; step back to the newest inbound one.
	accounts := a.profileAccounts(profile)
	if isOwnAddress(newest.From, accounts) {
		for i := len(thread) - 1; i >= 0; i-- {
			if !isOwnAddress(thread[i].From, accounts) {
				return thread[i], nil
			}
		}
	}
	return newest, nil
}

// isOwnAddress reports whether a From header belongs to this profile.
func isOwnAddress(from string, accounts map[string]bool) bool {
	addr := strings.ToLower(from)
	if parsed, err := mail.ParseAddress(from); err == nil {
		addr = strings.ToLower(parsed.Address)
	}
	for acct := range accounts {
		if acct != "" && strings.Contains(addr, acct) {
			return true
		}
	}
	return false
}

// replyRecipient returns the address to answer, preferring Reply-To.
func replyRecipient(m MailSummary) string {
	candidate := strings.TrimSpace(m.ReplyTo)
	if candidate == "" {
		candidate = strings.TrimSpace(m.From)
	}
	if candidate == "" {
		return ""
	}
	if parsed, err := mail.ParseAddress(candidate); err == nil {
		return parsed.Address
	}
	return candidate
}
