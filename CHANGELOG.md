# Changelog

All notable user-visible changes to `thunderbird-cli` are recorded here.

This file is the second product surface after the README. It should tell a serious operator what got faster, safer, or easier, and show the command that proves it.

## [Unreleased]

### Search UX

- `tb find` and `tb search` now tell you when a query has matches outside the requested `--since` / `--till` date window instead of only printing `No matches.`
- this makes partial-memory lookups safer when the remembered term is right but the time filter is wrong

## [3.0.5] - 2026-04-13

### Sync behavior

- `tb mail fetch --sync` now reuses an already-running Flatpak Betterbird/Thunderbird GUI session instead of failing just because the mail client is open
- native Thunderbird/Betterbird binaries still use the existing headless sync path

## [3.0.4] - 2026-04-12

### Mailbox triage

- added `tb mail unified` plus the top-level aliases `tb tail` and `tb head`
- unified inbox views now support `--ignore-account` and `--ignore-folder` so operators can suppress noisy accounts or mailbox paths during live triage
- recent-mail hunting no longer requires guessing which single INBOX received the reply before you can inspect it by Message-ID

### Sent verification

- fixed `tb mail sentcheck` for servers where IMAP header search misses a just-sent message even though the message is present in the Sent mailbox
- `sentcheck` now falls back to scanning recent Sent headers directly, so the verification path matches what `tb list 'Sent Items' --sync` already proves

## [3.0.3] - 2026-04-11

### Simpler mailbox commands

- added three obvious top-level aliases for common mail operations:
  - `tb list ...` -> `tb mail recent ...`
  - `tb read ...` -> `tb mail show ...`
  - `tb find ...` -> `tb mail search ...`
- updated the README, playbook, skill notes, and agent notes to make `list -> read -> find` the default operator flow

### Sync robustness

- `tb mail fetch --sync` and `tb mail recent --sync` now fail fast when the detected mail client is a Flatpak Betterbird/Thunderbird install running from a headless shell with no real GUI session
- sync runs now respect `TB_SYNC_TIMEOUT` (default `90s`) instead of hanging indefinitely behind GUI runtime noise

### Authentication checks

- added `tb mail authcheck` to send a real message and poll the receiving account over IMAP for the delivered authentication headers
- useful for proving what Gmail, Yahoo, Outlook, or another real mailbox thought about SPF, DKIM, and DMARC instead of inferring from DNS alone

### Sent verification

- added `tb mail sentcheck` to verify a sent copy online via IMAP instead of trusting local cache state
- this makes headless send auditable even while Betterbird is running and the local Sent folder has not synced yet
- Microsoft / Outlook direct send now skips the extra IMAP Sent append, because Outlook already stores a Sent copy server-side and double-appending created duplicates

## [3.0.2] - 2026-03-26

### Mailbox triage

- made `tb mail recent` the right first step for fresh-mail investigations:
  - added `--account`
  - added `--raw`
  - added `--sync`
- added `tb mail show --message-id '<...>'` so agents and operators can open the exact mail discovered from a recent-message tail without guessing the subject again
- updated the README, playbook, skill, and agent notes to make the intended workflow explicit:

```bash
tb mail fetch --profile default --sync
tb mail recent INBOX --profile default --account user@example.org --limit 20 --raw
tb mail recent "Junk Mail" --profile default --account user@example.org --limit 20 --raw
tb mail show --profile default --message-id '<message-id-from-recent>'
```

## [3.0.1] - 2026-03-25

### Headless send

- added direct headless send for non-OAuth SMTP/IMAP accounts whose encrypted passwords are already stored in the Thunderbird or Betterbird profile
- `tb mail compose --send --open=false` no longer has to fall back to GUI automation for standard password-backed accounts like `mail.gour.top`
- direct send still appends the exact RFC822 message to the real Sent folder after submission

### Capability reporting and docs

- `tb doctor`, `tb features`, README, SKILL, and playbook docs now describe secret-backed direct send more accurately instead of implying OAuth-only support
- runtime guidance now makes the NSS dependency story clearer for both OAuth and stored-password accounts

## [3.0.0] - 2026-03-25

`3.0.0` is the release where `tb` stopped requiring PostgreSQL for first-run usefulness and started acting like a real installable product instead of a promising local checkout.

### Headline changes

- SQLite is now the default cache backend.
- PostgreSQL is still supported, but only when explicitly selected.
- `tb doctor`, `tb features`, `tb version`, and `tb update` are first-class commands.
- release archives and `install.sh` now define the fast install/update path
- runtime capability reporting now explains whether direct OAuth send is compiled in and whether the current machine can actually use it

### Why this matters

Before `3.0.0`, the first serious question from a new user was usually some variant of:

- do I really need PostgreSQL just to search my own mail?
- why does this binary behave differently on different machines?
- how do I update this without rebuilding from source every time?

After `3.0.0`, the answers are much cleaner:

- no, SQLite is the default and works well for local mail operations
- the binary tells you the truth with `tb doctor` and `tb features`
- install and update are now designed as first-class operator commands

### Examples that are better now

Install and verify the machine in one short flow:

```bash
curl -fsSL https://raw.githubusercontent.com/avikalpa/thunderbird-cli/main/install.sh | sh
tb doctor
```

Refresh a profile and search without bringing PostgreSQL to the party:

```bash
tb mail fetch --profile default --sync
tb search "invoice" --profile default --limit 20
```

Inspect what the current build can actually do before trusting send automation:

```bash
tb features
tb doctor
```

Switch to PostgreSQL only when you actually want it:

```bash
export TB_STORE=postgres
export TB_PG_DSN='postgres://user:pass@localhost/tb_cli?sslmode=disable'
tb mail fetch --profile default --sync
tb search "release" --profile default
```

### Search and storage changes

- added a new SQLite cache backend using `FTS5`
- moved backend selection behind a store interface so SQLite can be the default and PostgreSQL can stay optional
- fixed the imported-profile account-directory issue by preferring usable profile-relative mail roots over stale absolute paths from another machine
- PostgreSQL search now uses the indexed full-text path instead of chained substring matches

### Runtime and release changes

- added `tb doctor` for profile detection, backend checks, and runtime diagnostics
- added `tb features` for a concise view of build/runtime capabilities
- added `tb version` for release metadata
- added `tb update` for GitHub-release-based upgrades on supported archive formats
- added `install.sh` for curl-based install on Linux and macOS
- release packaging now ships archives that include the binary plus license files and README

### Direct-send reporting changes

- direct OAuth send support is now reported explicitly by the binary instead of being implied
- unsupported builds degrade honestly instead of pretending the feature should work everywhere
- Linux release builds are intended to be the strongest path for direct provider-aware send
- portable builds remain useful for search/read workflows even when direct send is unavailable

### Documentation changes

- rewrote `README.md` as the actual operator manual, product pitch, and distribution guide
- moved fast install/update to the top of the README
- added explicit guidance for coding-agent workflows, including Codex and Claude Code usage patterns
- split repository licensing so code remains Apache-2.0 and Markdown docs are CC BY-SA 4.0

### Breaking expectations worth noting

- `TB_PG_DSN` is no longer required for normal use
- the default cache location is now the SQLite state directory, not PostgreSQL
- `tb update` is designed around release archives; Windows remains a manual-download path for now

## [0.2.0] - 2026-03-25

This release turned `tb` from a strong local-mail search tool into a credible headless mail operator for the accounts people already keep in Betterbird or Thunderbird.

### Added

- direct provider-aware headless send for:
  - Google / Gmail
  - Microsoft / Outlook / Hotmail / Office 365
  - Yahoo
- native NSS-backed secret decryption so `tb` can reuse the OAuth refresh tokens already stored in the Thunderbird or Betterbird profile
- direct SMTP `XOAUTH2` send for supported providers
- direct IMAP `XOAUTH2` append to the real server-side Sent folder after send
- provider-aware SMTP transport handling:
  - implicit TLS for Gmail and Yahoo
  - `STARTTLS` for Microsoft SMTP on port `587`
- tests for:
  - scalar `prefs.js` parsing
  - Sent-folder URI parsing
  - provider-specific default port selection

### Verified in practice

These flows were exercised against live configured accounts on this machine:

- Gmail direct headless send completed successfully
- Yahoo direct headless send completed successfully
- Outlook direct headless send completed successfully
- Yahoo self-send was verified on the live IMAP server in both `INBOX` and `Sent`
- Outlook self-send was verified on the live IMAP server in both `INBOX` and `Sent`

### Example

```bash
tb mail compose \
  --profile base_config \
  --from user@gmail.com \
  --to support@example.org \
  --cc audit@example.org \
  --subject "Support request" \
  --body "Hello" \
  --send --open=false
```
