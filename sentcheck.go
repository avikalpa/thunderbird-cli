package main

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/emersion/go-imap"
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
		if _, err := imapClient.Select(mailbox, true); err == nil {
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
