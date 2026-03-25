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
	googleOAuthIssuer    = "accounts.google.com"
	yahooOAuthIssuer     = "login.yahoo.com"
	microsoftOAuthIssuer = "login.microsoftonline.com"
	smtpTrySSLAlwaysTLS  = 3
	smtpTrySSLStartTLS   = 2
	smtpTrySSLNever      = 1
)

var providerConfigs = map[string]oauthProviderConfig{
	googleOAuthIssuer: {
		Name:          "Google",
		Hostname:      "oauth://accounts.google.com",
		ClientID:      "406964657835-aq8lmia8j95dhl1a2bvharmfk3t1hgqj.apps.googleusercontent.com",
		ClientSecret:  "kSmqreRr0qwBWJgbf5Y-PjSU",
		TokenEndpoint: "https://www.googleapis.com/oauth2/v3/token",
	},
	yahooOAuthIssuer: {
		Name:          "Yahoo",
		Hostname:      "oauth://login.yahoo.com",
		ClientID:      "dj0yJmk9NUtCTWFMNVpTaVJmJmQ9WVdrOVJ6UjVTa2xJTXpRbWNHbzlNQS0tJnM9Y29uc3VtZXJzZWNyZXQmeD0yYw--",
		ClientSecret:  "f2de6a30ae123cdbc258c15e0812799010d589cc",
		TokenEndpoint: "https://api.login.yahoo.com/oauth2/get_token",
	},
	microsoftOAuthIssuer: {
		Name:          "Microsoft",
		Hostname:      "oauth://login.microsoftonline.com",
		ClientID:      "9e5f94bc-e8a4-4e73-b8be-63364c29d753",
		TokenEndpoint: "https://login.microsoftonline.com/common/oauth2/v2.0/token",
	},
}

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
	TrySSL     int
	Issuer     string
	Scope      string
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
	RefreshToken     string `json:"refresh_token"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

type xoauth2SASLClient struct {
	Username string
	Token    string
}

type oauthProviderConfig struct {
	Name          string
	Hostname      string
	ClientID      string
	ClientSecret  string
	TokenEndpoint string
}

func (a *App) sendHeadlessly(profile Profile, to, cc, from, subject, body string) error {
	account, err := resolveSendAccount(profile, from)
	if err != nil {
		return err
	}
	switch directSendProvider(account) {
	case "google", "yahoo", "microsoft":
		return sendOAuthMessage(account, to, cc, subject, body)
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
		TrySSL:     atoiDefault(prefs[fmt.Sprintf("%s.%s.try_ssl", prefix, id)], 0),
		Issuer:     strings.TrimSpace(prefs[fmt.Sprintf("%s.%s.oauth2.issuer", prefix, id)]),
		Scope:      strings.TrimSpace(prefs[fmt.Sprintf("%s.%s.oauth2.scope", prefix, id)]),
	}
}

func directSendProvider(account sendAccountConfig) string {
	switch issuer := strings.ToLower(strings.TrimSpace(account.Outgoing.Issuer)); issuer {
	case googleOAuthIssuer:
		return "google"
	case yahooOAuthIssuer:
		return "yahoo"
	case microsoftOAuthIssuer:
		return "microsoft"
	}
	switch issuer := strings.ToLower(strings.TrimSpace(account.Incoming.Issuer)); issuer {
	case googleOAuthIssuer:
		return "google"
	case yahooOAuthIssuer:
		return "yahoo"
	case microsoftOAuthIssuer:
		return "microsoft"
	}
	return ""
}

func sendOAuthMessage(account sendAccountConfig, to, cc, subject, body string) error {
	refreshToken, err := oauthRefreshToken(account)
	if err != nil {
		return err
	}
	accessToken, err := refreshAccessToken(account, refreshToken)
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

func oauthRefreshToken(account sendAccountConfig) (string, error) {
	provider, ok := oauthConfigForAccount(account)
	if !ok {
		return "", fmt.Errorf("%w: %s", errDirectSendUnsupported, account.Identity.Email)
	}
	profilePath := account.Profile.AbsolutePath
	store, err := loadLogins(profilePath)
	if err != nil {
		return "", err
	}
	want := strings.ToLower(strings.TrimSpace(account.Outgoing.Username))
	for _, login := range store.Logins {
		if login.Hostname != provider.Hostname {
			continue
		}
		decodedUser, err := decryptNSSSecret(profilePath, login.EncryptedUsername)
		if err != nil {
			return "", fmt.Errorf("decrypt %s OAuth username: %w", provider.Name, err)
		}
		if strings.ToLower(strings.TrimSpace(decodedUser)) != want {
			continue
		}
		refresh, err := decryptNSSSecret(profilePath, login.EncryptedPassword)
		if err != nil {
			return "", fmt.Errorf("decrypt %s refresh token for %s: %w", provider.Name, account.Outgoing.Username, err)
		}
		return strings.TrimSpace(refresh), nil
	}
	return "", fmt.Errorf("no %s OAuth token stored for %s", provider.Name, account.Outgoing.Username)
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

func refreshAccessToken(account sendAccountConfig, refreshToken string) (string, error) {
	provider, ok := oauthConfigForAccount(account)
	if !ok {
		return "", fmt.Errorf("%w: %s", errDirectSendUnsupported, account.Identity.Email)
	}
	form := url.Values{
		"client_id":     {provider.ClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	}
	if provider.ClientSecret != "" {
		form.Set("client_secret", provider.ClientSecret)
	}
	if scope := oauthScopeForAccount(account); scope != "" {
		form.Set("scope", scope)
	}
	resp, err := http.PostForm(provider.TokenEndpoint, form)
	if err != nil {
		return "", fmt.Errorf("refresh %s token: %w", provider.Name, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read %s token response: %w", provider.Name, err)
	}
	var token googleTokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return "", fmt.Errorf("decode %s token response: %w", provider.Name, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if token.Error != "" {
			return "", fmt.Errorf("refresh %s token: %s: %s", provider.Name, token.Error, token.ErrorDescription)
		}
		return "", fmt.Errorf("refresh %s token: %s", provider.Name, strings.TrimSpace(string(body)))
	}
	if token.AccessToken == "" {
		return "", fmt.Errorf("refresh %s token returned no access token", provider.Name)
	}
	return token.AccessToken, nil
}

func oauthConfigForAccount(account sendAccountConfig) (oauthProviderConfig, bool) {
	for _, issuer := range []string{account.Outgoing.Issuer, account.Incoming.Issuer} {
		if cfg, ok := providerConfigs[strings.ToLower(strings.TrimSpace(issuer))]; ok {
			return cfg, true
		}
	}
	return oauthProviderConfig{}, false
}

func oauthScopeForAccount(account sendAccountConfig) string {
	for _, scope := range []string{account.Outgoing.Scope, account.Incoming.Scope} {
		scope = strings.TrimSpace(scope)
		if scope != "" {
			return scope
		}
	}
	return ""
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
	client, err := dialSMTP(account.Outgoing)
	if err != nil {
		return fmt.Errorf("connect SMTP %s: %w", addr, err)
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

func dialSMTP(server serverConfig) (*smtp.Client, error) {
	addr := net.JoinHostPort(server.Hostname, strconv.Itoa(server.Port))
	switch server.TrySSL {
	case smtpTrySSLStartTLS:
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		client, err := smtp.NewClient(conn, server.Hostname)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		if ok, _ := client.Extension("STARTTLS"); !ok {
			_ = client.Close()
			return nil, fmt.Errorf("server does not advertise STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: server.Hostname}); err != nil {
			_ = client.Close()
			return nil, err
		}
		return client, nil
	case smtpTrySSLNever:
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			return nil, err
		}
		client, err := smtp.NewClient(conn, server.Hostname)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return client, nil
	default:
		conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: server.Hostname})
		if err != nil {
			return nil, err
		}
		client, err := smtp.NewClient(conn, server.Hostname)
		if err != nil {
			_ = conn.Close()
			return nil, err
		}
		return client, nil
	}
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
	case incoming && strings.Contains(host, "office365.com"):
		return 993
	case !incoming && strings.Contains(host, "office365.com"):
		return 587
	case incoming && strings.Contains(host, "yahoo.com"):
		return 993
	case !incoming && strings.Contains(host, "yahoo.com"):
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
