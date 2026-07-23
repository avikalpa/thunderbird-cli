package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// credentialEntry mirrors one logins.json record. Thunderbird ignores unknown
// keys but requires this shape, so the full record is round-tripped rather than
// reconstructed from the narrower savedLogin used for reads.
type credentialEntry map[string]any

// setCredential stores an IMAP/SMTP password in the profile's logins.json,
// encrypted against that profile's own key4.db.
//
// This exists because credentials cannot be copied between profiles: the
// ciphertext is bound to the profile's SDR key. Migrating a profile therefore
// leaves any account whose password lived only in the old one unusable until
// the password is re-stored here.
func (a *App) setCredential(profileName, scheme, host, username, password string) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	scheme = strings.ToLower(strings.TrimSpace(scheme))
	switch scheme {
	case "imap", "smtp", "mailbox", "pop3":
	default:
		return fmt.Errorf("unsupported scheme %q (use imap, smtp, pop3 or mailbox)", scheme)
	}
	host = strings.TrimSpace(host)
	username = strings.TrimSpace(username)
	if host == "" || username == "" {
		return fmt.Errorf("--host and --username are required")
	}
	if password == "" {
		return fmt.Errorf("refusing to store an empty password")
	}

	encUser, err := encryptNSSSecret(profile.AbsolutePath, username)
	if err != nil {
		return fmt.Errorf("encrypt username: %w", err)
	}
	encPass, err := encryptNSSSecret(profile.AbsolutePath, password)
	if err != nil {
		return fmt.Errorf("encrypt password: %w", err)
	}

	path := filepath.Join(profile.AbsolutePath, "logins.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read logins.json: %w", err)
	}
	var store struct {
		NextID int               `json:"nextId"`
		Logins []credentialEntry `json:"logins"`
		Rest   map[string]any    `json:"-"`
	}
	var full map[string]any
	if err := json.Unmarshal(raw, &full); err != nil {
		return fmt.Errorf("parse logins.json: %w", err)
	}
	if err := json.Unmarshal(raw, &store); err != nil {
		return fmt.Errorf("parse logins.json logins: %w", err)
	}

	// Back up before touching the credential store.
	backup := fmt.Sprintf("%s.bak-%s", path, time.Now().UTC().Format("20060102-150405"))
	if err := os.WriteFile(backup, raw, 0o600); err != nil {
		return fmt.Errorf("write backup: %w", err)
	}

	wantHost := loginStoreHostname(scheme, host)
	now := time.Now().UnixMilli()
	replaced := false
	for _, entry := range store.Logins {
		if h, _ := entry["hostname"].(string); !strings.EqualFold(h, wantHost) {
			continue
		}
		existing, err := decryptNSSSecret(profile.AbsolutePath, asString(entry["encryptedUsername"]))
		if err != nil || !strings.EqualFold(strings.TrimSpace(existing), username) {
			continue
		}
		entry["encryptedPassword"] = encPass
		entry["timePasswordChanged"] = now
		replaced = true
		break
	}

	if !replaced {
		nextID := store.NextID
		if nextID <= 0 {
			nextID = len(store.Logins) + 1
		}
		store.Logins = append(store.Logins, credentialEntry{
			"id":                  nextID,
			"hostname":            wantHost,
			"httpRealm":           wantHost,
			"formSubmitURL":       nil,
			"usernameField":       "",
			"passwordField":       "",
			"encryptedUsername":   encUser,
			"encryptedPassword":   encPass,
			"guid":                fmt.Sprintf("{%s}", newCredentialGUID()),
			"encType":             1,
			"timeCreated":         now,
			"timeLastUsed":        now,
			"timePasswordChanged": now,
			"timesUsed":           1,
		})
		full["nextId"] = nextID + 1
	}

	full["logins"] = store.Logins
	out, err := json.Marshal(full)
	if err != nil {
		return fmt.Errorf("encode logins.json: %w", err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		return fmt.Errorf("write logins.json: %w", err)
	}

	action := "added"
	if replaced {
		action = "updated"
	}
	fmt.Printf("%s credential for %s (%s)\n", action, wantHost, username)
	fmt.Printf("backup: %s\n", backup)

	// Prove the round trip rather than assuming the write was usable.
	check, checkUser, err := loadStoredLoginCredential(profile.AbsolutePath, scheme, host, username)
	if err != nil {
		return fmt.Errorf("stored, but reading it back failed: %w", err)
	}
	_ = checkUser
	if check == "" {
		return fmt.Errorf("stored, but the credential read back empty")
	}
	fmt.Println("verified: tb can read the credential back")
	return nil
}

// listCredentials shows which hosts have stored credentials, never the secrets.
func (a *App) listCredentials(profileName string) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	store, err := loadLogins(profile.AbsolutePath)
	if err != nil {
		return err
	}
	if len(store.Logins) == 0 {
		fmt.Println("No stored credentials.")
		return nil
	}
	for _, login := range store.Logins {
		user, err := decryptNSSSecret(profile.AbsolutePath, login.EncryptedUsername)
		if err != nil {
			user = "<undecryptable>"
		}
		fmt.Printf("%s\t%s\n", login.Hostname, user)
	}
	return nil
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

// newCredentialGUID builds a RFC-4122-shaped identifier from the system's
// random source; Thunderbird only requires uniqueness.
func newCredentialGUID() string {
	b := make([]byte, 16)
	f, err := os.Open("/dev/urandom")
	if err == nil {
		defer f.Close()
		_, _ = io.ReadFull(f, b)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
