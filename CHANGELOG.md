# Changelog

All notable user-visible changes to `thunderbird-cli` are recorded here.

This file is not just a release ledger. It is the second place, after the README, where the project has to prove that it saves operator time in real situations.

## [0.2.0] - 2026-03-25

This release turns `tb` from a strong local-mail search tool into a credible headless mail operator for the accounts people already keep in Betterbird or Thunderbird.

### Added

- Direct provider-aware headless send for:
  - Google / Gmail
  - Microsoft / Outlook / Hotmail / Office 365
  - Yahoo
- Native NSS-backed secret decryption so `tb` can reuse the OAuth refresh tokens already stored in the Thunderbird or Betterbird profile.
- Direct SMTP `XOAUTH2` send for supported providers.
- Direct IMAP `XOAUTH2` append to the real server-side Sent folder after send.
- Provider-aware SMTP transport handling:
  - implicit TLS for Gmail and Yahoo
  - `STARTTLS` for Microsoft SMTP on port `587`
- Tests for:
  - scalar `prefs.js` parsing
  - Sent-folder URI parsing
  - provider-specific default port selection

### Changed

- `tb mail compose --send --open=false` now prefers a real provider-aware headless path before falling back to GUI automation.
- `prefs.js` parsing is no longer limited to quoted strings; integer and boolean prefs are now understood too.
- Documentation now treats the project as a product manual instead of a thin developer note.

### Verified In Practice

These flows were exercised against live configured accounts on this machine:

- Gmail direct headless send completed successfully.
- Yahoo direct headless send completed successfully.
- Outlook direct headless send completed successfully.
- Yahoo self-send was verified on the live IMAP server in both `INBOX` and `Sent`.
- Outlook self-send was verified on the live IMAP server in both `INBOX` and `Sent`.
- Gmail was already used earlier in this work to send a real Mentors support mail headlessly.

### Real Operator Examples

Search mail without reopening every mailbox file:

```bash
TB_PG_DSN=postgres://user:pass@localhost/dbname \
  tb search "support@mentors.debian.net" --profile base_config --refresh --limit 50
```

Read the exact message instead of the search hit snippet:

```bash
tb mail show --profile base_config --folder INBOX --query "Mail delivery failed" --limit 1 --thread
```

Send headlessly from a Gmail identity already stored in Betterbird:

```bash
tb mail compose --profile base_config \
  --from avikalpakundu@gmail.com \
  --to support@mentors.debian.net \
  --cc avikalpakundu@gmail.com \
  --subject "Mentors account activation/reset issue for avi@gour.top" \
  --body "Hello" \
  --send --open=false
```

Send headlessly from Outlook or Yahoo without opening the GUI:

```bash
tb mail compose --profile base_config --from avikalpa@outlook.com --to avikalpa@outlook.com --subject "outlook self-test" --body "headless send test" --send --open=false

tb mail compose --profile base_config --from avikalpa@yahoo.com --to avikalpa@yahoo.com --subject "yahoo self-test" --body "headless send test" --send --open=false
```

### Why This Release Matters

Before this release, `tb` could search mail well but still had to rely on Betterbird automation for most send workflows.

After this release, the supported-provider path is materially stronger:

- fewer moving parts
- less GUI dependence
- better behavior for coding agents
- real Sent-folder append on the server
- clearer failure modes when token refresh, SMTP auth, or IMAP append fail

That makes `tb` more useful for the exact class of tasks that tend to happen during operations work: incident follow-up, support escalation, audit trails, and agent-driven mail handling.
