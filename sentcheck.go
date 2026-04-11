package main

import (
	"fmt"
	"io"
	"net/mail"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

type sentcheckResult struct {
	Mailbox string
	Headers []string
}

func (a *App) sentcheck(profileName, from, subject, messageID, mailbox string, wait time.Duration, limit int) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	account, err := resolveSendAccount(profile, from)
	if err != nil {
		return err
	}
	res, err := pollSentMessages(account, subject, messageID, mailbox, wait, limit)
	if err != nil {
		return err
	}
	fmt.Printf("Mailbox: %s\n", res.Mailbox)
	for i, hdr := range res.Headers {
		if i > 0 {
			fmt.Println("---")
		}
		fmt.Print(hdr)
		if !strings.HasSuffix(hdr, "\n") {
			fmt.Println()
		}
	}
	return nil
}

func pollSentMessages(account sendAccountConfig, subject, messageID, mailbox string, wait time.Duration, limit int) (sentcheckResult, error) {
	if wait <= 0 {
		wait = 15 * time.Second
	}
	if limit <= 0 {
		limit = 1
	}
	if mailbox == "" {
		mailbox = sentMailboxName(account.Identity.SentFolder)
		if mailbox == "" {
			if directSendProvider(account) == "google" {
				mailbox = "[Gmail]/Sent Mail"
			} else {
				mailbox = "Sent"
			}
		}
	}

	imapClient, cleanup, err := openAccountIMAP(account)
	if err != nil {
		return sentcheckResult{}, err
	}
	defer cleanup()

	section := authcheckHeaderSection()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) || time.Now().Equal(deadline) {
		mbox, err := imapClient.Select(mailbox, true)
		if err == nil {
			crit := imap.NewSearchCriteria()
			if subject != "" {
				crit.Header.Add("Subject", subject)
			}
			if messageID != "" {
				crit.Header.Add("Message-ID", messageID)
			}
			ids, err := imapClient.Search(crit)
			if err == nil && len(ids) > 0 {
				sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
				if len(ids) > limit {
					ids = ids[len(ids)-limit:]
				}
				headers := make([]string, 0, len(ids))
				for _, id := range ids {
					seq := new(imap.SeqSet)
					seq.AddNum(id)
					hdr, err := fetchHeaderSection(imapClient, seq, section)
					if err != nil {
						return sentcheckResult{}, err
					}
					headers = append(headers, hdr)
				}
				return sentcheckResult{Mailbox: mailbox, Headers: headers}, nil
			}

			headers, err := fetchRecentSentHeaders(imapClient, mbox.Messages, section, limit)
			if err != nil {
				return sentcheckResult{}, err
			}
			matched := filterSentHeaders(headers, subject, messageID, limit)
			if len(matched) > 0 {
				return sentcheckResult{Mailbox: mailbox, Headers: matched}, nil
			}
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(2 * time.Second)
		_ = imapClient.Noop()
	}

	matchDesc := fmt.Sprintf("subject %q", subject)
	if messageID != "" && subject != "" {
		matchDesc = fmt.Sprintf("subject %q and message-id %q", subject, messageID)
	} else if messageID != "" {
		matchDesc = fmt.Sprintf("message-id %q", messageID)
	}
	return sentcheckResult{}, fmt.Errorf("no sent message with %s found in %q within %s", matchDesc, mailbox, wait)
}

func fetchRecentSentHeaders(imapClient *client.Client, total uint32, section *imap.BodySectionName, limit int) ([]string, error) {
	if total == 0 {
		return nil, nil
	}
	window := uint32(50)
	if limit > 0 {
		candidate := uint32(limit * 20)
		if candidate > window {
			window = candidate
		}
	}
	if window > total {
		window = total
	}
	start := total - window + 1
	seq := new(imap.SeqSet)
	seq.AddRange(start, total)
	msgs := make(chan *imap.Message, window)
	done := make(chan error, 1)
	go func() {
		done <- imapClient.Fetch(seq, []imap.FetchItem{section.FetchItem()}, msgs)
	}()
	var headers []string
	for msg := range msgs {
		if body := msg.GetBody(section); body != nil {
			data, err := io.ReadAll(body)
			if err != nil {
				return nil, fmt.Errorf("read sentcheck headers: %w", err)
			}
			headers = append(headers, string(data))
		}
	}
	if err := <-done; err != nil {
		return nil, err
	}
	return headers, nil
}

func filterSentHeaders(headers []string, subject, messageID string, limit int) []string {
	var matched []string
	wantSubject := strings.TrimSpace(subject)
	wantMessageID := strings.TrimSpace(messageID)
	for i := len(headers) - 1; i >= 0; i-- {
		hdr := headers[i]
		parsed, err := mail.ReadMessage(strings.NewReader(hdr))
		if err != nil {
			continue
		}
		if wantSubject != "" && parsed.Header.Get("Subject") != wantSubject {
			continue
		}
		if wantMessageID != "" && strings.TrimSpace(parsed.Header.Get("Message-ID")) != wantMessageID {
			continue
		}
		matched = append(matched, hdr)
		if limit > 0 && len(matched) >= limit {
			break
		}
	}
	return matched
}
