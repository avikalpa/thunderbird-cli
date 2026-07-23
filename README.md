# thunderbird-cli (`tb`)

Terminal-first operations for existing Thunderbird and Betterbird profiles.

`tb` is for people who already trust Thunderbird or Betterbird as the source of truth, but need a sharper operator layer for search, audits, scripting, and coding agents. It reads the profile you already have, keeps a fast local cache, and can send mail headlessly through the identities already configured in the mail client.

## Install In 20 Seconds

Recommended on Linux and macOS:

```bash
curl -fsSL https://raw.githubusercontent.com/avikalpa/thunderbird-cli/main/install.sh | sh
tb doctor
```

Fast update:

```bash
tb update
tb update --check
```

If `tb doctor` reports missing NSS runtime libraries on Linux, install the usual runtime packages for your distribution. On Debian and Ubuntu that is typically:

```bash
sudo apt install libnss3 libnspr4
```

Manual/source install remains available when you want the local checkout workflow or a platform-specific build:

```bash
git clone https://github.com/avikalpa/thunderbird-cli ~/gh/thunderbird-cli
cd ~/gh/thunderbird-cli
go build -o bin/tb ./...
./bin/tb doctor
```

### Keep one binary

`go build` leaves a binary in `./bin/tb`, `install.sh` writes to `~/.local/bin` by default, and `tb update` replaces only the copy it is running from. Since interactive and non-interactive shells often have different `PATH`s, `ssh host 'tb ...'` and your terminal can end up running different builds.

Pick one canonical path and install over it. `scripts/install-local.sh` builds the working tree and does this for you:

```bash
./scripts/install-local.sh                      # -> /usr/local/bin/tb
TB_INSTALL_DIR=~/.local/bin ./scripts/install-local.sh
```

`/usr/local/bin` is the default because it is on the non-interactive `PATH`. `tb doctor` lists every `tb` it can find and warns when they disagree.

## Why This Exists

Thunderbird and Betterbird are strong interactive mail clients. They are not strong operator CLIs.

Common pain points:

- repeated mail searches are slow and difficult to automate
- evidence collection through the GUI does not compose well with scripts or coding agents
- profile-stored credentials are trapped inside the client
- headless send is awkward even when the account is already configured
- local mail stores are too important to poke at with ad hoc one-off scripts

`tb` solves that without replacing Thunderbird or Betterbird:

- Thunderbird or Betterbird remains the source of truth
- your configured accounts stay where they are
- `tb` adds indexing, search, read, sync, and controlled send on top

## What Changed In 3.x

`3.x` is the release line where `tb` stopped assuming PostgreSQL for first-run usability.

The major design changes are deliberate:

- SQLite is now the default cache backend
- PostgreSQL is still supported, but only when explicitly requested
- `tb doctor`, `tb features`, and `tb update` are now first-class commands
- release archives and the installer script are part of the product surface
- runtime capability reporting is explicit, especially around direct secret-backed send support

That makes the tool easier to install, easier to carry to another machine, and easier to understand when a platform-specific edge case blocks a feature.

## Core Capabilities

`tb` does five things well:

1. discover Thunderbird and Betterbird profiles automatically
2. ingest mail into a fast local cache for repeated search
3. read matching messages and threads from local mail stores
4. trigger sync through the configured mail client when needed
5. send headlessly from profile-backed identities when the build and runtime support it

Cold-start search is now part of that contract. If a profile cache is empty, `tb find` does not sit there trying to ingest the whole profile before answering. It scans the real mailbox files directly, returns the hit, and leaves full cache hydration as an explicit operator step.

### No Silent Success

A mail tool that quietly answers from stale data is worse than one that fails, because you act on the answer. `tb` treats that as a correctness bug:

- `--sync` that cannot reach a display **fails** instead of falling through to the old cache
- a sync that changed nothing says so, instead of assuming it worked
- empty results name the folders actually searched, so "no matches" is never confused with "wrong mailbox"
- headless send prints the Message-ID and can confirm delivery from the server with `--verify`
- a reply that cannot carry its threading headers is refused rather than sent unthreaded
- `tb doctor` tells you which `tb` binary a shell will actually run, and warns when copies disagree

## For Coding Agents: Start With `tb q`

```bash
tb q "parcel signals badge"
```

One command, no decision tree. It searches every account and folder (Junk and Trash included, ranked below real mail), refreshes the cache if stale, ranks by relevance, and widens the match automatically when nothing hits — telling you which strategy worked.

Piped output is JSON, and every result carries the exact command to read it:

```json
{
  "message_id": "<...@zendesk.example>",
  "subject": "[Parcel] Re: Parcel Signals badge not showing up ...",
  "date": "2026-07-23T11:07:51Z",
  "read": "tb read --message-id \"<...@zendesk.example>\""
}
```

Results carry their scope (profile, account, folders searched, cache age), so an empty result is never confused with searching the wrong place. `--text` forces human output; `TB_JSON=1` forces JSON everywhere.

One call, not four:

```bash
tb q --today --important                              # what actually needs attention today
tb q "parcel signals badge" --thread --body            # the whole conversation, with bodies
tb q "electricity bill" --since 2019-07 --till 2019-07 --body  # a specific month, amounts included
```

`--since`/`--till` take `YYYY-MM-DD`, `YYYY-MM`, `today`, `yesterday`, or an offset (`7d`, `24h`, `3mo`); `--till` is inclusive. `--important` ranks by list headers, automated senders, whether you are in `To`/`Cc`, thread membership and deadline words — and prints `importance_why` for every message so the ranking can be checked.

## Simple Commands

The `mail ...` subcommands remain the full interface, but the fast operator path is now:

```bash
tb list --limit 20 --raw
tb list INBOX --account ops@example.org --limit 20 --raw
tb tail --account ops@example.org --limit 30 --raw --ignore-folder junk,trash
tb head --account ops@example.org --limit 10 --raw
tb read --message-id '<message-id-from-list>'
tb find --account ops@example.org --since 2026-01-01 --raw "keyword"
tb mail move --account ops@example.org --source-mailbox Junk --dest-mailbox INBOX --message-id '<message-id-from-list>'
```

`tb list` with no folder reads the inbox(es). Name a folder when you want a specific one; `--folder` also accepts a half-remembered name, so `--folder "acme sent"` resolves to `ImapMail/mail.acme-1.example/Sent Items`.

These are shorthands for:

- `tb list ...` -> `tb mail recent ...`
- `tb tail ...` -> `tb mail unified ...`
- `tb head ...` -> `tb mail unified --oldest ...`
- `tb read ...` -> `tb mail show ...`
- `tb find ...` -> `tb mail search ...`
- `tb search ...` still works as the older search shorthand

## Mailbox Triage First

If you are chasing a fresh reply, delivery-status notice, or account-recovery message, do not begin with a narrow keyword search unless you already know the exact sender or subject.

That mistake is how people miss important human replies. Automated account mail often starts from one address, then a maintainer or support person answers from another address with a different subject line.

Use this order instead:

```bash
tb doctor
tb mail fetch --profile default --sync
tb tail --profile default --account ops@example.org --limit 30 --raw --ignore-folder junk,trash
tb list "Junk Mail" --profile default --account ops@example.org --limit 20 --raw
tb read --profile default --message-id '<message-id-from-recent>'
tb find --profile default --account ops@example.org --since 2026-01-01 --raw "keyword you learned from the fresh mail"
```

That sequence is deliberate:

- `fetch --sync` makes the local profile fresh
- `tail ... --raw` gives you a unified inbox view across accounts without forcing you to guess which inbox caught the reply
- `--ignore-account` and `--ignore-folder` let you suppress noisy accounts or folders while triaging
- `show --message-id` inspects the exact mail you just found
- `search` comes after you know what you are really hunting

`--sync` needs a display. `tb` finds one in this order:

1. the GUI session of the current shell
2. the session of an already-running Betterbird/Thunderbird — this is what makes `--sync` work over plain ssh, and it is the only option that works for a Flatpak build
3. a temporary `Xvfb` display

If none is available, `--sync` **fails** rather than quietly answering from the stale cache. `tb doctor` reports which path applies before you start:

```bash
tb doctor | grep 'Sync display path'
```

You can cap sync duration with `TB_SYNC_TIMEOUT=30s` (default `90s`), and the standalone `tb mail sync --profile default --timeout 30s` command runs that refresh without touching the cache backend.

If you skip `fetch` on a profile that has never been indexed, `tb find` now uses a direct mailbox scan as the safety net. That is for getting the answer now. Use `tb mail fetch` or `tb mail search --refresh` when you want repeated searches to stay fast.

## Authentication Checks

When you need to prove what a receiving provider thinks about a message, use `authcheck` instead of stitching together `compose`, IMAP polling, and header scraping by hand:

```bash
tb mail authcheck \
  --profile default \
  --from ops@example.com \
  --to audit@example.org \
  --read-as audit@example.org \
  --wait 5m
```

What it does:

- sends a real message through the sender identity already configured in Thunderbird or Betterbird
- polls the receiving account over IMAP using the credentials or OAuth token already stored in the same profile
- prints the delivered message's authentication headers such as `Authentication-Results`, `Received-SPF`, and `DKIM-Signature`
- falls back to scanning recent mailbox headers directly when a provider's IMAP header search misses the delivered message, so placement is still provable on flaky servers such as Outlook.com

If the reader account is Gmail, Yahoo, or Outlook, `tb` chooses sensible mailbox defaults automatically. Override them with `--mailboxes` when a provider files the message somewhere unusual.

## Mailbox Training

When a provider accepts mail but files it in Junk or Spam, use `tb mail move` against the receiving account instead of relying on ad hoc GUI dragging:

```bash
tb mail move \
  --profile default \
  --account ops@example.com \
  --source-mailbox Junk \
  --dest-mailbox INBOX \
  --message-id '<1776769582505051702.1211401@example.com>'
```

This performs a real IMAP move on the remote mailbox and gives you a repeatable inbox-training primitive for providers such as Outlook.

## The Storage Model

Thunderbird and Betterbird keep the mail. `tb` keeps the operator cache.

Default cache backend:

- SQLite
- location: `~/.local/state/thunderbird-cli/...` unless overridden
- good for local single-user operation
- zero database service to install

Optional cache backend:

- PostgreSQL
- enable only when you explicitly want it
- useful when you already have PostgreSQL and want that operational model

Future direction:

- a Panorama-aware importer or adapter can be added later
- `tb` does not depend on Thunderbird's in-progress internal database work today

Choose PostgreSQL explicitly:

```bash
export TB_STORE=postgres
export TB_PG_DSN='postgres://user:pass@localhost/tb_cli?sslmode=disable'
```

Override the SQLite path explicitly:

```bash
export TB_SQLITE_PATH="$HOME/.local/state/thunderbird-cli/custom/index-v1.db"
```

## The Runtime Model

Profile data is treated as read-only.

Normal writes go only to:

- the configured cache backend
- temporary isolated send-profile clones when GUI fallback send is required
- the optional legacy `.tb-index.json` file only if you explicitly run `tb mail index`

`tb` does not rewrite your live mbox files, `.msf` files, Thunderbird SQLite files, or `prefs.js` during normal search work.

## Supported Send Paths

Direct headless send currently supports:

- Google / Gmail
- Microsoft / Outlook / Hotmail / Office 365
- Yahoo
- standard SMTP/IMAP accounts that store encrypted username/password credentials in the Thunderbird or Betterbird profile

For OAuth-backed providers, `tb mail compose --send --open=false` does this:

1. resolves the matching identity from `prefs.js`
2. decrypts the stored refresh token from the profile
3. refreshes an access token
4. submits the message over SMTP with `XOAUTH2`
5. appends the exact RFC822 message to the real Sent folder over IMAP
6. you can verify that sent copy online with `tb mail sentcheck --from <account> --subject "..."`

For stored-password accounts, `tb` does the same operator job without opening Betterbird:

1. resolves the matching identity from `prefs.js`
2. decrypts the stored SMTP and IMAP credentials from the profile with NSS
3. submits the message over SMTP using the server's advertised password auth mechanism
4. appends the exact RFC822 message to the real Sent folder over IMAP

When direct send is not available on a machine or in a build, `tb` falls back to the isolated Betterbird/Thunderbird automation path.

Inspect what the current binary can actually do:

```bash
tb features
tb doctor
```

## Platform Notes

Linux:

- release builds are intended to be the best-supported path
- direct secret-backed send depends on NSS runtime libraries being available
- `tb doctor` tells you whether the current binary and machine can do direct send

macOS and Windows:

- search, profile discovery, reading, and cache operations are the primary focus
- portable release builds may not include direct secret-backed send support yet
- use `tb features` and `tb doctor` instead of guessing

Windows:

- release archives are published
- `tb update` is not wired for Windows yet
- download a fresh release archive when updating there

## Requirements

- an existing Thunderbird or Betterbird profile
- Linux or macOS for the `install.sh` fast path
- Go 1.25+ if you are building from source
- NSS runtime libraries on Linux when you want direct secret-backed send

## First Five Minutes

List available profiles:

```bash
tb mail profiles
```

Hydrate the default cache from a profile:

```bash
tb mail fetch --profile default --sync
```

Search across the cached corpus:

```bash
tb search "invoice" --profile default --limit 20
tb search "support@company.example" --profile default --since 2026-01-01 --limit 20
```

Read the actual matching message:

```bash
tb mail show --profile default --folder INBOX --query "invoice" --limit 1 --thread
```

## Operator Workflows

### Search Without Reopening Every Mailbox File

```bash
tb mail fetch --profile default --sync
tb search "payment received" --profile default --limit 50
tb search "release train" --profile default --account ops@example.com --since 2026-01-01 --limit 50
```

### Refresh Before A Time-Sensitive Search

```bash
tb search --profile default --refresh --raw "Mail delivery failed"
```

### Full Rebuild When Integrity Matters More Than Speed

```bash
tb mail fetch --profile default --sync --prune --full
tb search "audit trail" --profile default --limit 100
```

### Open A Compose Window For Review

```bash
tb mail compose \
  --profile default \
  --from ops@example.com \
  --to support@example.org \
  --subject "Status update" \
  --body "Hello" \
  --open
```

### Send Headlessly Through A Configured Identity

```bash
tb mail compose \
  --profile default \
  --from ops@example.com \
  --to support@example.org \
  --cc audit@example.org \
  --subject "Status update" \
  --body "Hello" \
  --send --open=false
```

A successful send reports what it did, so you never have to guess whether it fired:

```
sent: ops@example.com -> support@example.org, audit@example.org
  message-id: <1784801299325454332.2671297@example.com>
  transport:  smtp+password via smtp.example.com:465
  sent copy:  appended to Sent Items
```

### Reply Into An Existing Thread

Support desks key off `In-Reply-To`/`References`. A reply without them usually opens a *new* ticket instead of appending to the open one.

```bash
tb mail compose \
  --profile default \
  --from ops@example.com \
  --to support@example.org \
  --subject "Re: Ticket 13421571" \
  --body-file reply.txt \
  --in-reply-to '<parent-message-id@example.org>' \
  --send --open=false --verify 60s
```

- `--body-file` takes a path, or `-` to read stdin. Use it for anything long enough that shell quoting over ssh becomes a hazard.
- `--verify <duration>` polls the Sent mailbox **on the server** for the Message-ID before returning. A stale local folder is not evidence of a failed send — do not re-send off a local-cache read.
- confirm the headers landed with `tb mail sentcheck --from ops@example.com --message-id '<id-from-the-send>'`.

Threading headers require a direct-send identity. The isolated-profile fallback drives the GUI composer and cannot set them, so `tb` refuses that combination rather than sending an unthreaded reply.

### Verify The Machine Before You Trust It

```bash
tb doctor
tb features
tb version
```

## Commands By Job

Inspect the environment:

```bash
tb doctor
tb features
tb version
```

Find profiles:

```bash
tb mail profiles
```

List folders:

```bash
tb mail folders --profile default
```

Hydrate or refresh the cache:

```bash
tb mail fetch --profile default --sync
tb mail fetch --profile default --account ops@example.com --folder INBOX
tb mail fetch --profile default --sync --prune --full
```

Search:

```bash
tb search "security review" --profile default --limit 25
tb search "security review" --profile default --account ops@example.com --since 2026-01-01 --till 2026-03-31 --limit 25
tb search --profile default --refresh --raw "support escalation"
```

Read full messages:

```bash
tb mail show --profile default --folder INBOX --query "security review" --limit 1
tb mail show --profile default --folder INBOX --query "security review" --limit 1 --thread
```

Send:

```bash
tb mail compose --profile default --to a@example.org --subject "Ping" --body "Hello"
tb mail compose --profile default --from ops@example.com --to a@example.org --subject "Ping" --body "Hello" --send --open=false
```

## Configuration

Most users need none of these on day one.

Profile discovery order:

1. `THUNDERBIRD_HOME`
2. `~/.var/app/eu.betterbird.Betterbird/.thunderbird`
3. `~/.var/app/org.mozilla.Thunderbird/.thunderbird`
4. `~/.thunderbird`

Relevant environment variables:

- `TB_STORE`: `sqlite` (default) or `postgres`
- `TB_SQLITE_PATH`: explicit SQLite cache file path
- `TB_PG_DSN`: PostgreSQL DSN when `TB_STORE=postgres`
- `THUNDERBIRD_HOME`: explicit profile root override
- `THUNDERBIRD_BIN`: explicit Thunderbird/Betterbird binary override
- `THUNDERBIRD_FLATPAK_ID`: explicit Flatpak app id override
- `TB_SYNC_TIMEOUT`: how long `--sync` lets the mail client fetch (default `90s`)
- `TB_AUTO_REFRESH_MINUTES`: refresh the cache when it is staler than this (default `10`, `0` disables)
- `TB_VERBOSE`: `1` to list every skipped folder instead of the one-line summary

## Coding-Agent Workflows

`tb` is designed to be driven by agents because it is deterministic, local, and inspectable.

Codex example:

```text
Use tb on this machine to inspect Thunderbird mail. Run `tb doctor`, refresh profile `default`, then search for "support@mentors.debian.net" since 2026-01-01 and return the top 10 results in raw mode.
```

Claude Code example:

```text
Use thunderbird-cli from the local checkout. Verify capabilities with `tb features`, hydrate the cache for profile `default`, then show the first full message in INBOX matching "Mail delivery failed".
```

Practical rule for agents:

- start with `tb doctor`
- use `tb mail fetch --sync` before time-sensitive hunts
- inspect `tb mail recent INBOX --raw` and `tb mail recent "Junk Mail" --raw` before trusting a guessed keyword
- when `recent` reveals the right mail, open it with `tb mail show --message-id '<...>'`
- prefer `tb search --raw` when another tool needs stable parseable output
- verify automated send through Sent/INBOX/Junk evidence, not SMTP success alone

Anonymized incident pattern worth following:

- an automated support thread started from one service address
- the real answer later arrived from a human maintainer address under a different subject
- keyword search on the original service address missed it
- the correct workflow was `fetch --sync`, `recent INBOX`, `recent Junk Mail`, then `show --message-id`

## Systemd Example

For machines where mail search should stay warm, a user timer is enough.

Service:

```ini
[Unit]
Description=Refresh thunderbird-cli cache

[Service]
Type=oneshot
ExecStart=%h/.local/bin/tb mail fetch --profile default --sync
```

Timer:

```ini
[Unit]
Description=Refresh thunderbird-cli cache every 30 minutes

[Timer]
OnBootSec=2m
OnUnitActiveSec=30m
Unit=thunderbird-cli-refresh.service

[Install]
WantedBy=timers.target
```

## Troubleshooting

### `tb doctor` says NSS runtime is missing

Install the runtime libraries for your distribution and rerun `tb doctor`.

On Debian and Ubuntu:

```bash
sudo apt install libnss3 libnspr4
```

### Direct send is unavailable in this build

That means this binary was built without NSS-backed secret decryption for direct send.

Use one of these paths:

- use the fallback send path
- build from source on Linux with cgo enabled and NSS development/runtime libraries available
- inspect the current build with `tb features`

### Search is empty on a profile that obviously has mail

Try the normal progression:

```bash
tb mail fetch --profile default --sync
tb search --profile default --refresh --raw "your query"
```

### `--sync` fails with "cannot sync: no GUI session in this shell"

Expected over plain ssh when nothing is running to join. `tb` fails here on purpose: the alternative is answering from a stale cache while looking like it fetched fresh mail.

Check what path is available before you start a time-sensitive hunt:

```bash
tb doctor | grep 'Sync display path'
```

Then pick one:

- open Betterbird/Thunderbird on the desktop session — `tb` will join it automatically
- point `THUNDERBIRD_BIN` at a native binary so `-headless` is usable
- install `Xvfb` for a temporary virtual display
- drop `--sync` and search the existing cache deliberately

### A send printed nothing — did it go out?

On 3.1.0 and later it prints the Message-ID, transport, and Sent-copy status. If you are unsure about an older send, **do not re-send off a local folder read** — the local mbox cache lags and has caused duplicate mail. Ask the server instead:

```bash
tb mail sentcheck --from ops@example.com --subject "the exact subject" --wait 30s
```

### I want PostgreSQL back

You still can. It is just no longer required for first-run usability.

```bash
export TB_STORE=postgres
export TB_PG_DSN='postgres://user:pass@localhost/tb_cli?sslmode=disable'
tb mail fetch --profile default --sync
tb search "invoice" --profile default
```

## Development

Run the local smoke suite:

```bash
./tests/run.sh
```

Local source builds are also how you should test capability differences that depend on the host machine, especially direct send and runtime library resolution.

## License

- code: Apache-2.0
- Markdown documentation: CC BY-SA 4.0

See `LICENSE` and `LICENSE-CC-BY-SA-4.0`.
