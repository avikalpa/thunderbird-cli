# tb playbook

Practical drills for using `tb` with the default SQLite cache, plus the optional PostgreSQL path when you explicitly need it.

## Prep

```sh
go build -o bin/tb ./...
tb doctor
tb mail profiles
```

## Fast operator aliases

```sh
tb list INBOX --account user@example.com --limit 20 --raw
tb tail --account user@example.com --limit 30 --raw --ignore-folder junk,trash
tb head --account user@example.com --limit 10 --raw
tb read --message-id '<message-id>'
tb find --account user@example.com --since 2026-01-01 --raw "keyword"
```

Alias mapping:

- `tb list ...` -> `tb mail recent ...`
- `tb tail ...` -> `tb mail unified ...`
- `tb head ...` -> `tb mail unified --oldest ...`
- `tb read ...` -> `tb mail show ...`
- `tb find ...` -> `tb mail search ...`

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
tb tail --profile default --account user@example.com --limit 30 --raw --ignore-folder junk,trash
tb list "Junk Mail" --profile default --account user@example.com --limit 20 --raw
tb read --profile default --message-id '<message-id-from-recent>'

If `tb mail fetch --sync` is using a Flatpak Betterbird/Thunderbird install with a real GUI session, it now reuses the already-running app. From a headless shell with no real GUI session, it still fails fast with a clear error instead of disappearing into GTK noise. In that case either:

- point `THUNDERBIRD_BIN` at a native Thunderbird/Betterbird binary, or
- skip `--sync` and inspect the already-synced local mailbox cache

You can tune the sync cap with `TB_SYNC_TIMEOUT=30s` (default `90s`).
```

Only after you know what the fresh mail looks like:

```sh
tb find --profile default --account user@example.com --since 2026-01-01 --raw "keyword"
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

Authentication verdict from the receiver side:

```sh
tb mail authcheck \
  --profile base_config \
  --from avikalpa@gour.top \
  --to avikalpakundu@gmail.com \
  --read-as avikalpakundu@gmail.com \
  --wait 5m
```

Use this when you need the receiving provider's actual headers instead of guessing from DNS alone.

## Safety reminders

- `tb doctor` is the first diagnostic command, not an afterthought.
- Searches read the cache; `tb mail fetch` and `tb search --refresh` are the paths that touch mailboxes.
- Avoid `--prune` unless you want strict cache mirroring.
- Prefer `--account` and date bounds before forcing folder names.
- Delivery status notifications may reflect downstream forwarding failures even when local submission succeeded.
