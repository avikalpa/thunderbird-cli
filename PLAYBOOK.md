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
tb list --limit 20 --raw
tb list INBOX --account user@example.com --limit 20 --raw
tb tail --account user@example.com --limit 30 --raw --ignore-folder junk,trash
tb head --account user@example.com --limit 10 --raw
tb read --message-id '<message-id>'
tb find --account user@example.com --since 2026-01-01 --raw "keyword"
```

`tb list` with no folder reads the inbox(es). `--folder` accepts a
half-remembered name (`--folder "acme sent"` -> `.../Sent Items`), and an empty
result always names the folders it actually read.

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

`--sync` picks a display in this order: the GUI session of this shell, then the
session of an already-running Betterbird/Thunderbird (so plain ssh works), then
a temporary `Xvfb` display. If none is available it fails rather than answering
from the stale cache. Check which applies with `tb doctor | grep 'Sync display path'`.

When it does fail, either:

- open the mail client on the desktop session so `tb` can join it,
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

Reply into an existing thread, with a body from a file and server-side
confirmation:

```sh
tb mail compose --profile default \
  --from user@example.com --to support@example.org \
  --subject "Re: Ticket 13421571" \
  --body-file /path/to/reply.txt \
  --in-reply-to '<parent-message-id@example.org>' \
  --send --open=false --verify 60s
```

A send reports its Message-ID, transport, and Sent-copy status. If you are ever
unsure whether a send fired, ask the server — never a local folder read:

```sh
tb mail sentcheck --from user@example.com --message-id '<id-from-the-send>'
```

Authentication verdict from the receiver side:

```sh
tb mail authcheck \
  --profile default \
  --from ops@example.com \
  --to audit@example.org \
  --read-as audit@example.org \
  --wait 5m
```

Use this when you need the receiving provider's actual headers instead of guessing from DNS alone.

## Safety reminders

- `tb doctor` is the first diagnostic command, not an afterthought. Its `Sync display path` line tells you whether `--sync` can work at all from this shell.
- Searches read the cache; `tb mail fetch` and `tb search --refresh` are the paths that touch mailboxes.
- `--sync` now fails rather than falling back to the stale cache. That is deliberate: drop `--sync` when you mean to search the cache.
- Avoid `--prune` unless you want strict cache mirroring.
- Prefer `--account` and date bounds before forcing folder names.
- Delivery status notifications may reflect downstream forwarding failures even when local submission succeeded.
- A stale local Sent folder is **not** evidence of a failed send. Verify with `tb mail sentcheck` or `--verify`; never re-send off a local-cache read.
