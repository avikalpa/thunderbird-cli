package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-sasl"
)

var errDirectSendUnsupported = errors.New("direct headless send unsupported for this account")

const (
	googleOAuthIssuer       = "accounts.google.com"
	googleTokenEndpoint     = "https://www.googleapis.com/oauth2/v3/token"
	googleThunderbirdAppID  = "406964657835-aq8lmia8j95dhl1a2bvharmfk3t1hgqj.apps.googleusercontent.com"
	googleThunderbirdSecret = "kSmqreRr0qwBWJgbf5Y-PjSU"
)

type identityConfig struct {
	ID         string
	Email      string
	FullName   string
	SMTPServer string
	SentFolder string
}

type serverConfig struct {
	ID         string
	Hostname   string
	Port       int
	Username   string
	AuthMethod int
	Issuer     string
}

type sendAccountConfig struct {
	Profile  Profile
	Identity identityConfig
	Incoming serverConfig
	Outgoing serverConfig
}

type loginStore struct {
	Logins []savedLogin `json:"logins"`
}

type savedLogin struct {
	Hostname          string `json:"hostname"`
	EncryptedUsername string `json:"encryptedUsername"`
	EncryptedPassword string `json:"encryptedPassword"`
	HTTPRealm         string `json:"httpRealm"`
	FormSubmitURL     string `json:"formSubmitURL"`
}

type googleTokenResponse struct {
	AccessToken      string `json:"access_token"`
	ExpiresIn        int    `json:"expires_in"`
	TokenType        string `json:"token_type"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type xoauth2SASLClient struct {
	Username string
	Token    string
}

func (a *App) sendHeadlessly(profile Profile, to, cc, from, subject, body string) error {
	account, err := resolveSendAccount(profile, from)
	if err != nil {
		return err
	}
	switch directSendProvider(account) {
	case "google":
		return sendGoogleMessage(account, to, cc, subject, body)
	default:
		return fmt.Errorf("%w: %s", errDirectSendUnsupported, account.Identity.Email)
	}
}

func resolveSendAccount(profile Profile, from string) (sendAccountConfig, error) {
	prefsPath := filepath.Join(profile.AbsolutePath, "prefs.js")
	prefs, err := parsePrefs(prefsPath)
	if err != nil {
		return sendAccountConfig{}, fmt.Errorf("read prefs: %w", err)
	}

	accountIDs := splitCSV(prefs["mail.accountmanager.accounts"])
	if len(accountIDs) == 0 {
		return sendAccountConfig{}, fmt.Errorf("no configured accounts in %s", prefsPath)
	}

	identityID, accountID, err := pickIdentity(prefs, accountIDs, from)
	if err != nil {
		return sendAccountConfig{}, err
	}
	serverID := prefs[fmt.Sprintf("mail.account.%s.server", accountID)]
	if serverID == "" {
		return sendAccountConfig{}, fmt.Errorf("account %s has no incoming server", accountID)
	}

	identity := identityConfig{
		ID:         identityID,
		Email:      strings.TrimSpace(prefs[fmt.Sprintf("mail.identity.%s.useremail", identityID)]),
		FullName:   strings.TrimSpace(prefs[fmt.Sprintf("mail.identity.%s.fullName", identityID)]),
		SMTPServer: strings.TrimSpace(prefs[fmt.Sprintf("mail.identity.%s.smtpServer", identityID)]),
		SentFolder: strings.TrimSpace(prefs[fmt.Sprintf("mail.identity.%s.fcc_folder", identityID)]),
	}
	if identity.Email == "" {
		return sendAccountConfig{}, fmt.Errorf("identity %s has no useremail", identityID)
	}
	if identity.SMTPServer == "" {
		identity.SMTPServer = strings.TrimSpace(prefs["mail.smtp.defaultserver"])
	}
	if identity.SMTPServer == "" {
		return sendAccountConfig{}, fmt.Errorf("identity %s has no smtp server", identityID)
	}

	incoming := buildServerConfig(prefs, "mail.server", serverID)
	outgoing := buildServerConfig(prefs, "mail.smtpserver", identity.SMTPServer)
	if incoming.Hostname == "" {
		return sendAccountConfig{}, fmt.Errorf("incoming server %s has no hostname", incoming.ID)
	}
	if outgoing.Hostname == "" {
		return sendAccountConfig{}, fmt.Errorf("smtp server %s has no hostname", outgoing.ID)
	}
	if incoming.Username == "" {
		incoming.Username = identity.Email
	}
	if outgoing.Username == "" {
		outgoing.Username = identity.Email
	}
	if incoming.Port == 0 {
		incoming.Port = defaultPort(incoming.Hostname, true)
	}
	if outgoing.Port == 0 {
		outgoing.Port = defaultPort(outgoing.Hostname, false)
	}

	return sendAccountConfig{
		Profile:  profile,
		Identity: identity,
		Incoming: incoming,
		Outgoing: outgoing,
	}, nil
}

func pickIdentity(prefs map[string]string, accountIDs []string, from string) (identityID, accountID string, err error) {
	want := strings.ToLower(strings.TrimSpace(from))
	defaultAccount := strings.TrimSpace(prefs["mail.accountmanager.defaultaccount"])

	for _, accID := range orderedAccounts(accountIDs, defaultAccount) {
		identityIDs := splitCSV(prefs[fmt.Sprintf("mail.account.%s.identities", accID)])
		for _, id := range identityIDs {
			email := strings.ToLower(strings.TrimSpace(prefs[fmt.Sprintf("mail.identity.%s.useremail", id)]))
			if email == "" {
				continue
			}
			if want == "" {
				return id, accID, nil
			}
			if email == want {
				return id, accID, nil
			}
		}
	}
	if want != "" {
		return "", "", fmt.Errorf("no configured identity matches %q", from)
	}
	return "", "", fmt.Errorf("no usable Thunderbird/Betterbird identity found")
}

func orderedAccounts(accountIDs []string, defaultAccount string) []string {
	if defaultAccount == "" {
		return accountIDs
	}
	seen := map[string]struct{}{}
	out := []string{defaultAccount}
	seen[defaultAccount] = struct{}{}
	for _, accID := range accountIDs {
		if _, ok := seen[accID]; ok {
			continue
		}
		out = append(out, accID)
	}
	return out
}

func buildServerConfig(prefs map[string]string, prefix, id string) serverConfig {
	return serverConfig{
		ID:         id,
		Hostname:   strings.TrimSpace(prefs[fmt.Sprintf("%s.%s.hostname", prefix, id)]),
		Port:       atoiDefault(prefs[fmt.Sprintf("%s.%s.port", prefix, id)], 0),
		Username:   strings.TrimSpace(prefs[fmt.Sprintf("%s.%s.username", prefix, id)]),
		AuthMethod: atoiDefault(prefs[fmt.Sprintf("%s.%s.authMethod", prefix, id)], 0),
		Issuer:     strings.TrimSpace(prefs[fmt.Sprintf("%s.%s.oauth2.issuer", prefix, id)]),
	}
}

func directSendProvider(account sendAccountConfig) string {
	if strings.EqualFold(account.Outgoing.Issuer, googleOAuthIssuer) ||
		strings.EqualFold(account.Incoming.Issuer, googleOAuthIssuer) ||
		strings.EqualFold(account.Outgoing.Hostname, "smtp.gmail.com") ||
		strings.EqualFold(account.Incoming.Hostname, "imap.gmail.com") {
		return "google"
	}
	return ""
}

func sendGoogleMessage(account sendAccountConfig, to, cc, subject, body string) error {
	refreshToken, err := googleRefreshToken(account.Profile.AbsolutePath, account.Outgoing.Username)
	if err != nil {
		return err
	}
	accessToken, err := googleAccessToken(refreshToken)
	if err != nil {
		return err
	}
	rawMsg, recipients, err := buildOutgoingMessage(account, to, cc, subject, body)
	if err != nil {
		return err
	}
	if err := smtpSendXOAUTH2(account, accessToken, rawMsg, recipients); err != nil {
		return err
	}
	if err := appendSentMessage(account, accessToken, rawMsg); err != nil {
		return fmt.Errorf("message sent but failed to append to %q: %w", sentMailboxName(account.Identity.SentFolder), err)
	}
	return nil
}

func googleRefreshToken(profilePath, username string) (string, error) {
	store, err := loadLogins(profilePath)
	if err != nil {
		return "", err
	}
	want := strings.ToLower(strings.TrimSpace(username))
	for _, login := range store.Logins {
		if login.Hostname != "oauth://accounts.google.com" {
			continue
		}
		decodedUser, err := decryptNSSSecret(profilePath, login.EncryptedUsername)
		if err != nil {
			return "", fmt.Errorf("decrypt google username: %w", err)
		}
		if strings.ToLower(strings.TrimSpace(decodedUser)) != want {
			continue
		}
		refresh, err := decryptNSSSecret(profilePath, login.EncryptedPassword)
		if err != nil {
			return "", fmt.Errorf("decrypt google refresh token for %s: %w", username, err)
		}
		return strings.TrimSpace(refresh), nil
	}
	return "", fmt.Errorf("no Google OAuth token stored for %s", username)
}

func loadLogins(profilePath string) (loginStore, error) {
	var store loginStore
	data, err := os.ReadFile(filepath.Join(profilePath, "logins.json"))
	if err != nil {
		return store, fmt.Errorf("read logins.json: %w", err)
	}
	if err := json.Unmarshal(data, &store); err != nil {
		return store, fmt.Errorf("parse logins.json: %w", err)
	}
	return store, nil
}

func googleAccessToken(refreshToken string) (string, error) {
	form := url.Values{
		"client_id":     {googleThunderbirdAppID},
		"client_secret": {googleThunderbirdSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	resp, err := http.PostForm(googleTokenEndpoint, form)
	if err != nil {
		return "", fmt.Errorf("refresh Google token: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read Google token response: %w", err)
	}
	var token googleTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return "", fmt.Errorf("decode Google token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if token.Error != "" {
			return "", fmt.Errorf("refresh Google token: %s: %s", token.Error, token.ErrorDescription)
		}
		return "", fmt.Errorf("refresh Google token: %s", strings.TrimSpace(string(body)))
	}
	if token.AccessToken == "" {
		return "", fmt.Errorf("refresh Google token returned no access token")
	}
	return token.AccessToken, nil
}

func buildOutgoingMessage(account sendAccountConfig, to, cc, subject, body string) ([]byte, []string, error) {
	toAddrs, err := parseAddressListCSV(to)
	if err != nil {
		return nil, nil, fmt.Errorf("parse --to: %w", err)
	}
	ccAddrs, err := parseAddressListCSV(cc)
	if err != nil {
		return nil, nil, fmt.Errorf("parse --cc: %w", err)
	}
	fromAddr := &mail.Address{Name: account.Identity.FullName, Address: account.Identity.Email}
	recipients := flattenAddresses(toAddrs, ccAddrs)
	if len(recipients) == 0 {
		return nil, nil, fmt.Errorf("no recipients specified")
	}

	var buf bytes.Buffer
	writeHeader := func(name, value string) {
		if value == "" {
			return
		}
		buf.WriteString(name)
		buf.WriteString(": ")
		buf.WriteString(value)
		buf.WriteString("\r\n")
	}

	writeHeader("Date", time.Now().Format(time.RFC1123Z))
	writeHeader("Message-ID", newMessageID(account.Identity.Email))
	writeHeader("From", fromAddr.String())
	writeHeader("To", joinAddressHeader(toAddrs))
	if len(ccAddrs) > 0 {
		writeHeader("Cc", joinAddressHeader(ccAddrs))
	}
	if subject != "" {
		writeHeader("Subject", mime.QEncoding.Encode("utf-8", subject))
	}
	writeHeader("MIME-Version", "1.0")
	writeHeader("Content-Type", `text/plain; charset="utf-8"`)
	writeHeader("Content-Transfer-Encoding", "quoted-printable")
	buf.WriteString("\r\n")

	qp := quotedprintable.NewWriter(&buf)
	if _, err := qp.Write([]byte(normalizeBody(body))); err != nil {
		return nil, nil, fmt.Errorf("encode message body: %w", err)
	}
	if err := qp.Close(); err != nil {
		return nil, nil, fmt.Errorf("finalize message body: %w", err)
	}

	return buf.Bytes(), recipients, nil
}

func parseAddressListCSV(value string) ([]*mail.Address, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	addrs, err := mail.ParseAddressList(value)
	if err != nil {
		return nil, err
	}
	return addrs, nil
}

func flattenAddresses(parts ...[]*mail.Address) []string {
	var out []string
	for _, group := range parts {
		for _, addr := range group {
			out = append(out, addr.Address)
		}
	}
	return out
}

func joinAddressHeader(addrs []*mail.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(addrs))
	for _, addr := range addrs {
		parts = append(parts, addr.String())
	}
	return strings.Join(parts, ", ")
}

func newMessageID(email string) string {
	domain := "localhost"
	if parts := strings.Split(strings.TrimSpace(email), "@"); len(parts) == 2 && parts[1] != "" {
		domain = parts[1]
	}
	return fmt.Sprintf("<%d.%d@%s>", time.Now().UnixNano(), os.Getpid(), domain)
}

func normalizeBody(body string) string {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	body = strings.ReplaceAll(body, "\r", "\n")
	return strings.ReplaceAll(body, "\n", "\r\n")
}

func smtpSendXOAUTH2(account sendAccountConfig, accessToken string, rawMsg []byte, recipients []string) error {
	addr := net.JoinHostPort(account.Outgoing.Hostname, strconv.Itoa(account.Outgoing.Port))
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: account.Outgoing.Hostname})
	if err != nil {
		return fmt.Errorf("connect SMTP %s: %w", addr, err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, account.Outgoing.Hostname)
	if err != nil {
		return fmt.Errorf("init SMTP client: %w", err)
	}
	defer client.Close()

	if err := client.Auth(xoauth2SMTPAuth{
		Username: account.Outgoing.Username,
		Token:    accessToken,
	}); err != nil {
		return fmt.Errorf("SMTP auth for %s: %w", account.Outgoing.Username, err)
	}
	if err := client.Mail(account.Identity.Email); err != nil {
		return fmt.Errorf("SMTP MAIL FROM %s: %w", account.Identity.Email, err)
	}
	for _, rcpt := range recipients {
		if err := client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("SMTP RCPT TO %s: %w", rcpt, err)
		}
	}
	wc, err := client.Data()
	if err != nil {
		return fmt.Errorf("SMTP DATA: %w", err)
	}
	if _, err := wc.Write(rawMsg); err != nil {
		_ = wc.Close()
		return fmt.Errorf("SMTP write message: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("SMTP finalize message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("SMTP quit: %w", err)
	}
	return nil
}

func appendSentMessage(account sendAccountConfig, accessToken string, rawMsg []byte) error {
	mailbox := sentMailboxName(account.Identity.SentFolder)
	if mailbox == "" {
		if directSendProvider(account) == "google" {
			mailbox = "[Gmail]/Sent Mail"
		} else {
			mailbox = "Sent"
		}
	}

	addr := net.JoinHostPort(account.Incoming.Hostname, strconv.Itoa(account.Incoming.Port))
	imapClient, err := client.DialTLS(addr, &tls.Config{ServerName: account.Incoming.Hostname})
	if err != nil {
		return fmt.Errorf("connect IMAP %s: %w", addr, err)
	}
	defer imapClient.Logout()

	if err := imapClient.Authenticate(&xoauth2SASLClient{
		Username: account.Incoming.Username,
		Token:    accessToken,
	}); err != nil {
		return fmt.Errorf("IMAP auth for %s: %w", account.Incoming.Username, err)
	}

	var buf bytes.Buffer
	buf.Write(rawMsg)
	if err := imapClient.Append(mailbox, nil, time.Now(), &buf); err != nil {
		return fmt.Errorf("append to %s: %w", mailbox, err)
	}
	return nil
}

func sentMailboxName(uri string) string {
	uri = strings.TrimSpace(uri)
	if uri == "" {
		return ""
	}
	u, err := url.Parse(uri)
	if err != nil {
		return ""
	}
	path := strings.TrimPrefix(u.Path, "/")
	if path == "" && u.Opaque != "" {
		path = strings.TrimPrefix(u.Opaque, "/")
	}
	if path == "" {
		return ""
	}
	decoded, err := url.PathUnescape(path)
	if err != nil {
		return path
	}
	return decoded
}

func defaultPort(host string, incoming bool) int {
	host = strings.ToLower(strings.TrimSpace(host))
	switch {
	case incoming && strings.Contains(host, "gmail.com"):
		return 993
	case !incoming && strings.Contains(host, "gmail.com"):
		return 465
	default:
		if incoming {
			return 993
		}
		return 465
	}
}

func atoiDefault(value string, fallback int) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func (c *xoauth2SASLClient) Start() (string, []byte, error) {
	return "XOAUTH2", []byte(fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", c.Username, c.Token)), nil
}

func (c *xoauth2SASLClient) Next(challenge []byte) ([]byte, error) {
	if len(challenge) == 0 {
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected XOAUTH2 challenge: %s", strings.TrimSpace(string(challenge)))
}

type xoauth2SMTPAuth struct {
	Username string
	Token    string
}

func (a xoauth2SMTPAuth) Start(*smtp.ServerInfo) (string, []byte, error) {
	return "XOAUTH2", []byte(fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", a.Username, a.Token)), nil
}

func (a xoauth2SMTPAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	if len(fromServer) == 0 {
		return nil, sasl.ErrUnexpectedServerChallenge
	}
	return nil, fmt.Errorf("unexpected SMTP XOAUTH2 challenge: %s", strings.TrimSpace(string(fromServer)))
}
