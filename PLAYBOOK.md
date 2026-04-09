# tb playbook

Practical drills for using `tb` with the default SQLite cache, plus the optional PostgreSQL path when you explicitly need it.

## Prep

```sh
go build -o bin/tb ./...
tb doctor
tb mail profiles
```

## Hydrate the default cache

Incremental refresh with sync:

```sh
tb mail fetch --profile default --sync
```

Narrow to one account:

```sh
tb mail fetch --profile default --account user@example.com --sync
```

Strict mirror rebuild:

```sh
tb mail fetch --profile default --sync --prune --full
```

## Core searches

Wide scan across all folders:

```sh
tb search "invoice" --profile default --limit 50
```

Account scoped:

```sh
tb search "meeting" --profile default --account user@example.com --limit 100
```

Date window:

```sh
tb search "contract" --profile default --since 2023-01-01 --till 2024-06-30
```

Refresh before searching:

```sh
tb search "shipment" --profile default --refresh --limit 50
```

## Mailbox triage for fresh replies

Use this when you suspect a new reply landed but the sender or subject may have changed.

```sh
tb mail fetch --profile default --sync
tb mail recent INBOX --profile default --account user@example.com --limit 20 --raw
tb mail recent "Junk Mail" --profile default --account user@example.com --limit 20 --raw
tb mail show --profile default --message-id '<message-id-from-recent>'

If `tb mail fetch --sync` is using a Flatpak Betterbird/Thunderbird install from a headless shell, it now fails fast with a clear error instead of disappearing into GTK noise. In that case either:

- point `THUNDERBIRD_BIN` at a native Thunderbird/Betterbird binary, or
- skip `--sync` and inspect the already-synced local mailbox cache

You can tune the sync cap with `TB_SYNC_TIMEOUT=30s` (default `90s`).
```

Only after you know what the fresh mail looks like:

```sh
tb search --profile default --account user@example.com --since 2026-01-01 --raw "keyword"
```

## Full-message inspection

```sh
tb mail show --profile default --folder INBOX --query "subject fragment" --limit 1
tb mail show --profile default --folder INBOX --query "subject fragment" --limit 1 --thread
tb mail show --profile default --message-id '<exact-message-id>'
```

## Optional PostgreSQL path

```sh
export TB_STORE=postgres
export TB_PG_DSN='postgres://user:pass@localhost/tb_cli?sslmode=disable'
tb mail fetch --profile default --sync
tb search "invoice" --profile default --limit 50
```

## Compose and send

Open composer for review:

```sh
tb mail compose --profile default --to a@b --subject "Check-in" --body "text"
```

Send headlessly:

```sh
tb mail compose --profile default --from user@example.com --to a@b --subject "Send now" --body "text" --send --open=false
```

This path now covers both:

- OAuth-backed accounts such as Gmail, Outlook, and Yahoo
- standard SMTP/IMAP accounts whose encrypted passwords are already stored in the Thunderbird or Betterbird profile

## Safety reminders

- `tb doctor` is the first diagnostic command, not an afterthought.
- Searches read the cache; `tb mail fetch` and `tb search --refresh` are the paths that touch mailboxes.
- Avoid `--prune` unless you want strict cache mirroring.
- Prefer `--account` and date bounds before forcing folder names.
- Delivery status notifications may reflect downstream forwarding failures even when local submission succeeded.
