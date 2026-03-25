# thunderbird-cli (`tb`)

Terminal-first mail operations for existing Thunderbird and Betterbird profiles.

`tb` is built for people who already trust Thunderbird or Betterbird as the source of truth, but need something sharper for automation, search, audits, scripting, and coding agents. It indexes mail into Postgres for fast search, reads full messages without UI clicking, and can send mail headlessly through the credentials already stored in your profile.

## Quick Install

Fastest install from GitHub:

```bash
go install github.com/avikalpa/thunderbird-cli@latest
```

Fastest update:

```bash
go install github.com/avikalpa/thunderbird-cli@latest
```

Local checkout workflow:

```bash
git clone https://github.com/avikalpa/thunderbird-cli ~/gh/thunderbird-cli
cd ~/gh/thunderbird-cli
go build -o bin/tb ./...
```

Local checkout update:

```bash
cd ~/gh/thunderbird-cli
git pull --ff-only
go build -o bin/tb ./...
```

Sanity check:

```bash
tb mail profiles
```

Release notes live in [CHANGELOG.md](./CHANGELOG.md).

## Why This Exists

Thunderbird and Betterbird are excellent interactive mail clients. They are not excellent operator CLIs.

The pain points are consistent:

- finding evidence across years of mail is slow in the UI
- repeating the same searches for support, operations, or compliance work is tedious
- coding agents need structured commands, not GUI clicks
- headless send is awkward even when the account is already configured in the mail client
- scripting around local mail stores is fragile if every tool invents its own parser

`tb` solves that by staying inside the reality you already have:

- your Thunderbird or Betterbird profile stays authoritative
- your configured accounts stay configured there
- `tb` only adds fast search, automation, and controlled headless send

## What `tb` Does Well

- discovers Thunderbird and Betterbird profiles automatically
- ingests mail into Postgres for fast repeated search
- searches across all folders without rereading mbox files every time
- reads full messages and threads from local mail stores
- drives headless sync through Thunderbird or Betterbird when needed
- sends mail headlessly through profile-stored auth material
- appends sent mail to the real server-side Sent folder for auditability
- gives coding agents a stable CLI instead of screen-driving the mail UI

## Supported Headless Send Providers

Direct provider-aware headless send is currently implemented for:

- Google / Gmail
- Microsoft / Outlook / Hotmail / Office 365
- Yahoo

For those providers, `tb mail compose --send --open=false` does this:

1. resolves the exact Thunderbird or Betterbird identity from `prefs.js`
2. decrypts the stored OAuth refresh token from `logins.json` via NSS
3. refreshes an access token using the same built-in provider details Betterbird uses
4. sends over SMTP with `XOAUTH2`
5. appends the exact RFC822 message to the configured Sent folder over IMAP with `XOAUTH2`

Unsupported providers fall back to the older isolated-profile automation path based on `Xvfb` and `xdotool`.

## Safety Model

`tb` is designed to be useful under operator pressure without quietly taking ownership of your mail data.

What it does not do:

- it does not rewrite your live Thunderbird or Betterbird profile
- it does not mutate mbox, `.msf`, SQLite, or prefs files during normal search workflows
- it does not invent a parallel account store

What it does write:

- Postgres cache tables: `tb_messages`, `tb_meta`
- optional legacy local index if you explicitly use `tb mail index`
- temporary isolated send-profile clones for unsupported-provider fallback sends

That split matters. Thunderbird remains your mail client. `tb` is the operator layer on top.

## Who This Is For

`tb` is for:

- operators investigating incidents through email
- developers who need to inspect local mail with scripts
- people who trust Thunderbird or Betterbird but want a real CLI
- coding-agent users who want mail tasks to be reproducible and inspectable
- anyone who would rather query mail like a system than click through it like a filing cabinet

## Requirements

- Go 1.24+
- Thunderbird or Betterbird profile
- Postgres for indexed search
- NSS runtime libraries available on the machine

Profile root detection order:

1. `THUNDERBIRD_HOME`
2. `~/.thunderbird`
3. `~/.var/app/eu.betterbird.Betterbird/.thunderbird`
4. `~/.var/app/org.mozilla.Thunderbird/.thunderbird`

If you use Betterbird Flatpak, the usual profile root is:

```bash
~/.var/app/eu.betterbird.Betterbird/.thunderbird
```

Postgres DSN is read from `TB_PG_DSN`.
You can keep it in a local `.env` file; `.env.example` shows the shape.

## First Five Minutes

Build or install `tb`, then:

```bash
tb mail profiles
```

If Postgres is available:

```bash
export TB_PG_DSN='postgres://user:pass@localhost/dbname'
tb mail fetch --profile base_config --sync
```

Then search:

```bash
tb search "invoice" --profile base_config --limit 20
tb search "mentors.debian.net" --profile base_config --account avi@gour.top --limit 20
```

Then inspect a message:

```bash
tb mail show --profile base_config --folder INBOX --query "Mentors" --limit 1 --thread
```

## Operator Quickstart

### 1. Hydrate the cache

```bash
TB_PG_DSN=postgres://user:pass@localhost/dbname \
  tb mail fetch --profile base_config --sync
```

Important behavior:

- default ingest is incremental
- `--full-rescan` forces a full rebuild
- `--prune` removes DB rows not seen in the current scan and implies a full rebuild
- `--account` and `--folder` can narrow the ingest scope

### 2. Search without touching mailboxes again

```bash
tb search "payment received" --profile base_config --limit 50
tb search "ghostty" --profile base_config --account avikalpakundu@gmail.com --since 2026-01-01 --limit 50
tb search --profile base_config --refresh --raw "support@mentors.debian.net"
```

### 3. Read the full evidence, not just the hit line

```bash
tb mail show --profile base_config --folder INBOX --query "Mail delivery failed" --limit 1 --thread
```

### 4. Send headlessly when the account is already configured

Open a compose window:

```bash
tb mail compose --profile base_config \
  --from avikalpakundu@gmail.com \
  --to support@mentors.debian.net \
  --subject "Support request" \
  --body "Hello" 
```

Send without opening the GUI:

```bash
tb mail compose --profile base_config \
  --from avikalpakundu@gmail.com \
  --to support@mentors.debian.net \
  --cc avikalpakundu@gmail.com \
  --subject "Mentors account activation/reset issue for avi@gour.top" \
  --body "Hello" \
  --send --open=false
```

Provider-specific examples:

```bash
tb mail compose --profile base_config \
  --from avikalpakundu@gmail.com \
  --to avikalpakundu@gmail.com \
  --subject "gmail self-test" \
  --body "headless send test" \
  --send --open=false


tb mail compose --profile base_config \
  --from avikalpa@outlook.com \
  --to avikalpa@outlook.com \
  --subject "outlook self-test" \
  --body "headless send test" \
  --send --open=false


tb mail compose --profile base_config \
  --from avikalpa@yahoo.com \
  --to avikalpa@yahoo.com \
  --subject "yahoo self-test" \
  --body "headless send test" \
  --send --open=false
```

## Commands By Job

### Discover profiles

```bash
tb mail profiles
```

### List folders

```bash
tb mail folders --profile base_config
```

### Ingest mail into Postgres

```bash
tb mail fetch --profile base_config --sync
tb mail fetch --profile base_config --account avikalpakundu@gmail.com --sync
tb mail fetch --profile base_config --folder INBOX --sync --max-messages 200
```

### Search the cache

```bash
tb search "invoice"
tb search "shipment" --profile base_config --refresh
tb search "contract" --profile base_config --since 2025-01-01 --till 2025-12-31
```

### Read a full message or thread

```bash
tb mail show --profile base_config --folder INBOX --query "subject fragment" --limit 1
tb mail show --profile base_config --folder INBOX --query "subject fragment" --limit 1 --thread
```

### Compose or send

```bash
tb mail compose --to a@b --subject "Update" --body "text"
tb mail send --profile base_config --from avikalpakundu@gmail.com --to a@b --subject "Update" --body "text" --send --open=false
```

## Use With Coding Agents

This tool is explicitly useful for coding-agent workflows because it turns local mail operations into stable commands.

### Example: Codex

Prompt:

```text
Use ~/gh/thunderbird-cli to search my Betterbird profile for all Mentors-related mail since 2026-03-01, then show me the newest rejection or activation-related thread.
```

Likely command flow:

```bash
export TB_PG_DSN='postgres://user:pass@localhost/dbname'
~/gh/thunderbird-cli/bin/tb mail fetch --profile base_config --sync --account avi@gour.top
~/gh/thunderbird-cli/bin/tb search "mentors.debian.net" --profile base_config --account avi@gour.top --since 2026-03-01 --limit 20
~/gh/thunderbird-cli/bin/tb mail show --profile base_config --folder INBOX --query "Next step: Confirm your email address" --limit 1 --thread
```

### Example: Claude Code

Prompt:

```text
Search the local Betterbird profile for delivery-status notifications or support replies related to support@mentors.debian.net, and use thunderbird-cli instead of trying to drive the GUI.
```

Likely command flow:

```bash
export TB_PG_DSN='postgres://user:pass@localhost/dbname'
~/gh/thunderbird-cli/bin/tb search "support@mentors.debian.net" --profile base_config --refresh --limit 50
~/gh/thunderbird-cli/bin/tb search "Mail delivery failed" --profile base_config --limit 20
~/gh/thunderbird-cli/bin/tb mail show --profile base_config --folder INBOX --query "Mail delivery failed" --limit 1 --thread
```

### Why agents benefit from `tb`

- search output is scriptable
- `--raw` output is easy to feed back into an LLM loop
- direct send avoids GUI flakiness for supported providers
- profile discovery and identity selection are explicit
- the operator can audit the same commands afterward

## Systemd Automation

Hourly refresh service:

`~/.config/systemd/user/tb-fetch.service`

```ini
[Unit]
Description=tb mail fetch (profile base_config)

[Service]
Type=oneshot
Environment=TB_PG_DSN=postgres://user:pass@localhost/dbname
ExecStart=%h/go/bin/tb mail fetch --profile base_config --sync
```

If you want to pin the profile root explicitly:

```ini
Environment=THUNDERBIRD_HOME=%h/.var/app/eu.betterbird.Betterbird/.thunderbird
```

Timer:

`~/.config/systemd/user/tb-fetch.timer`

```ini
[Unit]
Description=Run tb mail fetch hourly

[Timer]
OnCalendar=hourly
Persistent=true

[Install]
WantedBy=timers.target
```

Enable it:

```bash
systemctl --user daemon-reload
systemctl --user enable --now tb-fetch.timer
```

## Troubleshooting

### `TB_PG_DSN not set`

Search and fetch use Postgres.
Set it explicitly:

```bash
export TB_PG_DSN='postgres://user:pass@localhost/dbname'
```

### Betterbird is already running, but headless sync fails

That is a profile-lock issue, not necessarily a mail failure.
The live GUI instance owns the profile. Direct provider-aware send can still work even when a second sync attempt fails.

### Mail sent successfully but local Betterbird cache does not show it yet

That is normal when:

- the GUI instance has not synced that account yet
- `.msf` files lag behind the live server state
- you are relying on offline cache instead of direct IMAP verification

For supported providers, `tb` appends to the real Sent folder on the server as part of the send path.
If that append fails, the command fails.

### Unsupported provider send path

If the provider is not yet implemented directly, `tb` falls back to an isolated Betterbird automation path. That keeps the live desktop safer, but it is slower and more fragile than the direct OAuth path.

## Design Notes

`tb` chooses an intentionally conservative architecture.

- Thunderbird or Betterbird owns account setup
- NSS continues to own secret storage and decryption format
- Postgres owns indexed search
- `tb` owns orchestration, search UX, and headless send glue

That means the tool is boring in the right places. It works with the mail client you already trust instead of replacing it with another half-mail-client.

## Release Discipline

- README is the product manual, the pitch, and the first proof that the software respects the reader's time.
- CHANGELOG is the second marketing surface and should explain what materially improved for operators.
- release notes should show visible user wins, not just internal refactors.

Read [CHANGELOG.md](./CHANGELOG.md) before upgrading if you care about behavior changes.

## Tests

```bash
./tests/run.sh
```

For quick verification during development:

```bash
go test ./...
go build -o bin/tb ./...
```

## License

Apache License 2.0.

Copyright 2025 Avikalpa Kundu.
