package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

type authcheckResult struct {
	Mailbox string
	Headers string
}

func (a *App) authcheck(profileName, from, to, readAs, subject, body string, wait time.Duration, mailboxes []string) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	from = strings.TrimSpace(from)
	to = strings.TrimSpace(to)
	readAs = strings.TrimSpace(readAs)
	if from == "" {
		return fmt.Errorf("authcheck: --from is required")
	}
	if to == "" {
		return fmt.Errorf("authcheck: --to is required")
	}
	if readAs == "" {
		readAs = to
	}
	if subject == "" {
		stamp := time.Now().UTC().Format("20060102T150405Z")
		subject = fmt.Sprintf("[tb-authcheck %s]", stamp)
	}
	if body == "" {
		body = fmt.Sprintf("Authentication check for %s via %s at %s", to, from, time.Now().UTC().Format(time.RFC3339))
	}
	if wait <= 0 {
		wait = 2 * time.Minute
	}

	fmt.Printf("Submitting authcheck message\n")
	fmt.Printf("From: %s\n", from)
	fmt.Printf("To: %s\n", to)
	fmt.Printf("Read-As: %s\n", readAs)
	fmt.Printf("Subject: %s\n", subject)
	if err := a.sendHeadlessly(profile, to, "", from, subject, body); err != nil {
		return fmt.Errorf("authcheck send: %w", err)
	}

	reader, err := resolveSendAccount(profile, readAs)
	if err != nil {
		return fmt.Errorf("authcheck reader account %s: %w", readAs, err)
	}
	if len(mailboxes) == 0 {
		mailboxes = defaultAuthcheckMailboxes(reader)
	}
	res, err := pollAuthHeaders(reader, subject, wait, mailboxes)
	if err != nil {
		return err
	}
	fmt.Printf("Mailbox: %s\n", res.Mailbox)
	fmt.Print(res.Headers)
	return nil
}

func defaultAuthcheckMailboxes(account sendAccountConfig) []string {
	host := strings.ToLower(strings.TrimSpace(account.Incoming.Hostname))
	switch {
	case strings.Contains(host, "gmail.com"):
		return []string{"INBOX", "[Gmail]/All Mail", "[Gmail]/Spam"}
	case strings.Contains(host, "yahoo.com"):
		return []string{"INBOX", "Bulk", "Spam"}
	case strings.Contains(host, "office365.com"):
		return []string{"INBOX", "Junk Email", "Junk"}
	default:
		return []string{"INBOX", "Junk Mail", "Spam"}
	}
}

func pollAuthHeaders(account sendAccountConfig, subject string, wait time.Duration, mailboxes []string) (authcheckResult, error) {
	imapClient, cleanup, err := openAccountIMAP(account)
	if err != nil {
		return authcheckResult{}, err
	}
	defer cleanup()

	section := authcheckHeaderSection()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		for _, mailbox := range mailboxes {
			if _, err := imapClient.Select(mailbox, true); err != nil {
				continue
			}
			crit := imap.NewSearchCriteria()
			crit.Header.Add("Subject", subject)
			ids, err := imapClient.Search(crit)
			if err != nil || len(ids) == 0 {
				continue
			}
			seq := new(imap.SeqSet)
			seq.AddNum(ids[len(ids)-1])
			headers, err := fetchHeaderSection(imapClient, seq, section)
			if err != nil {
				return authcheckResult{}, err
			}
			return authcheckResult{Mailbox: mailbox, Headers: headers}, nil
		}
		time.Sleep(10 * time.Second)
		_ = imapClient.Noop()
	}
	return authcheckResult{}, fmt.Errorf("authcheck: no message with subject %q arrived within %s", subject, wait)
}

func openAccountIMAP(account sendAccountConfig) (*client.Client, func(), error) {
	imapClient, err := dialIMAP(account.Incoming)
	if err != nil {
		addr := fmt.Sprintf("%s:%d", account.Incoming.Hostname, account.Incoming.Port)
		return nil, nil, fmt.Errorf("connect IMAP %s: %w", addr, err)
	}
	cleanup := func() { _ = imapClient.Logout() }
	switch directSendProvider(account) {
	case "google", "yahoo", "microsoft":
		refreshToken, err := oauthRefreshToken(account)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		accessToken, err := refreshAccessToken(account, refreshToken)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		if err := imapClient.Authenticate(&xoauth2SASLClient{
			Username: account.Incoming.Username,
			Token:    accessToken,
		}); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("IMAP auth for %s: %w", account.Incoming.Username, err)
		}
	default:
		username, password, err := loadStoredLoginCredential(account.Profile.AbsolutePath, "imap", account.Incoming.Hostname, account.Incoming.Username)
		if err != nil {
			cleanup()
			return nil, nil, err
		}
		if err := imapClient.Login(username, password); err != nil {
			cleanup()
			return nil, nil, fmt.Errorf("IMAP login for %s: %w", username, err)
		}
	}
	return imapClient, cleanup, nil
}

func authcheckHeaderSection() *imap.BodySectionName {
	return &imap.BodySectionName{
		Peek: true,
		BodyPartName: imap.BodyPartName{
			Specifier: imap.HeaderSpecifier,
			Fields: []string{
				"Authentication-Results",
				"ARC-Authentication-Results",
				"Received-SPF",
				"DKIM-Signature",
				"From",
				"To",
				"Subject",
				"Message-ID",
				"Date",
				"Delivered-To",
				"Return-Path",
			},
		},
	}
}

func fetchHeaderSection(imapClient *client.Client, seq *imap.SeqSet, section *imap.BodySectionName) (string, error) {
	msgs := make(chan *imap.Message, 1)
	done := make(chan error, 1)
	go func() {
		done <- imapClient.Fetch(seq, []imap.FetchItem{section.FetchItem()}, msgs)
	}()
	var out strings.Builder
	for msg := range msgs {
		if body := msg.GetBody(section); body != nil {
			data, err := io.ReadAll(body)
			if err != nil {
				return "", fmt.Errorf("read authcheck headers: %w", err)
			}
			out.Write(data)
		}
	}
	if err := <-done; err != nil {
		return "", err
	}
	if out.Len() == 0 {
		return "", fmt.Errorf("authcheck: fetched message headers were empty")
	}
	return out.String(), nil
}
