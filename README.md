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
- runtime capability reporting is explicit, especially around direct OAuth send support

That makes the tool easier to install, easier to carry to another machine, and easier to understand when a platform-specific edge case blocks a feature.

## Core Capabilities

`tb` does five things well:

1. discover Thunderbird and Betterbird profiles automatically
2. ingest mail into a fast local cache for repeated search
3. read matching messages and threads from local mail stores
4. trigger sync through the configured mail client when needed
5. send headlessly from profile-backed identities when the build and runtime support it

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

Direct provider-aware headless send currently supports:

- Google / Gmail
- Microsoft / Outlook / Hotmail / Office 365
- Yahoo

When direct send is available, `tb mail compose --send --open=false` does this:

1. resolves the matching identity from `prefs.js`
2. decrypts the stored refresh token from the profile
3. refreshes an access token
4. submits the message over SMTP with `XOAUTH2`
5. appends the exact RFC822 message to the real Sent folder over IMAP

When direct send is not available on a machine or in a build, `tb` falls back to the isolated Betterbird/Thunderbird automation path.

Inspect what the current binary can actually do:

```bash
tb features
tb doctor
```

## Platform Notes

Linux:

- release builds are intended to be the best-supported path
- direct OAuth send depends on NSS runtime libraries being available
- `tb doctor` tells you whether the current binary and machine can do direct send

macOS and Windows:

- search, profile discovery, reading, and cache operations are the primary focus
- portable release builds may not include direct OAuth send support yet
- use `tb features` and `tb doctor` instead of guessing

Windows:

- release archives are published
- `tb update` is not wired for Windows yet
- download a fresh release archive when updating there

## Requirements

- an existing Thunderbird or Betterbird profile
- Linux or macOS for the `install.sh` fast path
- Go 1.25+ if you are building from source
- NSS runtime libraries on Linux when you want direct provider-aware send

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
2. `~/.thunderbird`
3. `~/.var/app/eu.betterbird.Betterbird/.thunderbird`
4. `~/.var/app/org.mozilla.Thunderbird/.thunderbird`

Relevant environment variables:

- `TB_STORE`: `sqlite` (default) or `postgres`
- `TB_SQLITE_PATH`: explicit SQLite cache file path
- `TB_PG_DSN`: PostgreSQL DSN when `TB_STORE=postgres`
- `THUNDERBIRD_HOME`: explicit profile root override
- `THUNDERBIRD_BIN`: explicit Thunderbird/Betterbird binary override
- `THUNDERBIRD_FLATPAK_ID`: explicit Flatpak app id override

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
- prefer `tb search --raw` when another tool needs stable parseable output
- verify automated send through Sent/INBOX/Junk evidence, not SMTP success alone

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

That means this binary was built without NSS-backed direct OAuth support.

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
