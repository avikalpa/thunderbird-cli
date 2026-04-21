package main

import (
	"fmt"
	"io"
	"net/mail"
	"strings"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

type recentHeader struct {
	SeqNum uint32
	Header string
}

func (a *App) moveMail(profileName, accountEmail, sourceMailbox, destMailbox, subject, messageID string, limit int) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	if strings.TrimSpace(accountEmail) == "" {
		return fmt.Errorf("move: --account is required")
	}
	if strings.TrimSpace(sourceMailbox) == "" {
		return fmt.Errorf("move: --source-mailbox is required")
	}
	if strings.TrimSpace(destMailbox) == "" {
		return fmt.Errorf("move: --dest-mailbox is required")
	}
	if strings.TrimSpace(subject) == "" && strings.TrimSpace(messageID) == "" {
		return fmt.Errorf("move: one of --subject or --message-id is required")
	}
	if limit <= 0 {
		limit = 1
	}

	account, err := resolveSendAccount(profile, accountEmail)
	if err != nil {
		return err
	}
	moved, err := moveMailboxMessages(account, sourceMailbox, destMailbox, subject, messageID, limit)
	if err != nil {
		return err
	}
	fmt.Printf("Moved %d message(s) from %s to %s\n", moved, sourceMailbox, destMailbox)
	return nil
}

func moveMailboxMessages(account sendAccountConfig, sourceMailbox, destMailbox, subject, messageID string, limit int) (int, error) {
	imapClient, cleanup, err := openAccountIMAP(account)
	if err != nil {
		return 0, err
	}
	defer cleanup()

	mbox, err := imapClient.Select(sourceMailbox, false)
	if err != nil {
		return 0, fmt.Errorf("select %s: %w", sourceMailbox, err)
	}

	ids, err := searchMailboxMessages(imapClient, subject, messageID)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		headers, err := fetchRecentHeaderMessages(imapClient, mbox.Messages, authcheckHeaderSection(), limit)
		if err != nil {
			return 0, err
		}
		ids = filterHeaderSeqNums(headers, subject, messageID, limit)
	}
	if len(ids) == 0 {
		matchDesc := fmt.Sprintf("subject %q", subject)
		if messageID != "" && subject != "" {
			matchDesc = fmt.Sprintf("subject %q and message-id %q", subject, messageID)
		} else if messageID != "" {
			matchDesc = fmt.Sprintf("message-id %q", messageID)
		}
		return 0, fmt.Errorf("move: no message with %s found in %q", matchDesc, sourceMailbox)
	}

	seq := new(imap.SeqSet)
	for _, id := range ids {
		seq.AddNum(id)
	}
	if err := imapClient.Move(seq, destMailbox); err != nil {
		return 0, fmt.Errorf("move %s -> %s: %w", sourceMailbox, destMailbox, err)
	}
	return len(ids), nil
}

func searchMailboxMessages(imapClient *client.Client, subject, messageID string) ([]uint32, error) {
	crit := imap.NewSearchCriteria()
	if subject != "" {
		crit.Header.Add("Subject", subject)
	}
	if messageID != "" {
		crit.Header.Add("Message-ID", messageID)
	}
	ids, err := imapClient.Search(crit)
	if err != nil {
		return nil, fmt.Errorf("search mailbox: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	if len(ids) > 1 {
		ids = ids[len(ids)-1:]
	}
	return ids, nil
}

func fetchRecentHeaderMessages(imapClient *client.Client, total uint32, section *imap.BodySectionName, limit int) ([]recentHeader, error) {
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
	var headers []recentHeader
	for msg := range msgs {
		if body := msg.GetBody(section); body != nil {
			data, err := io.ReadAll(body)
			if err != nil {
				return nil, fmt.Errorf("read mailbox headers: %w", err)
			}
			headers = append(headers, recentHeader{
				SeqNum: msg.SeqNum,
				Header: string(data),
			})
		}
	}
	if err := <-done; err != nil {
		return nil, err
	}
	return headers, nil
}

func filterHeaderSeqNums(headers []recentHeader, subject, messageID string, limit int) []uint32 {
	var matched []uint32
	wantSubject := strings.TrimSpace(subject)
	wantMessageID := strings.TrimSpace(messageID)
	for i := len(headers) - 1; i >= 0; i-- {
		hdr := headers[i]
		parsed, err := mail.ReadMessage(strings.NewReader(hdr.Header))
		if err != nil {
			continue
		}
		if wantSubject != "" && parsed.Header.Get("Subject") != wantSubject {
			continue
		}
		if wantMessageID != "" && strings.TrimSpace(parsed.Header.Get("Message-ID")) != wantMessageID {
			continue
		}
		matched = append(matched, hdr.SeqNum)
		if limit > 0 && len(matched) >= limit {
			break
		}
	}
	return matched
}
