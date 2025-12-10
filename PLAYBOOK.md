# tb playbook

Practical drills for using `tb` with a Postgres cache (no direct mbox searches once hydrated).

## Prep
```sh
export TB_PG_DSN=postgres://user:pass@localhost/dbname
go build -o bin/tb ./...
tb mail profiles
```

## Hydrate the cache
- Full ingest with headless sync (full rescan):  
  `tb mail fetch --profile base_config --sync --full-rescan`
- Narrow to one account:  
  `tb mail fetch --profile base_config --account user@example.com --sync`
- Safety-first mirror (only if you want DB rows removed when missing on disk):  
  `tb mail fetch --profile base_config --sync --prune --full-rescan`

## Core searches (Postgres-only)
- Wide scan across all folders:  
  `tb search "invoice" --profile base_config --limit 50`
- Account scoped:  
  `tb search "meeting" --profile base_config --account user@example.com --limit 100`
- Date window:  
  `tb search "contract" --profile base_config --since 2023-01-01 --till 2024-06-30`
- Fuzzy tokens (all must appear):  
  `tb search --profile base_config --fuzzy "payment confirmation"`
- Force incremental refresh before search:  
  `tb search "shipment" --profile base_config --refresh --limit 0`
- Full rebuild before search (slow):  
  `tb search "shipment" --profile base_config --full-rescan --limit 0`

## Full-message inspection
- First matching message:  
  `tb mail show --profile base_config --folder ImapMail/example.com/INBOX --query "subject fragment" --limit 1`
- Thread (same subject in folder):  
  `tb mail show --profile base_config --folder ImapMail/example.com/INBOX --query "subject fragment" --limit 1 --thread`

## Timeline drills (stress the index)
- Common word, no cap:  
  `tb search "receipt" --profile base_config --limit 0`
- Account + date clamp:  
  `tb search "statement" --profile base_config --account user@example.com --since 2022-01-01 --till 2025-01-01 --limit 0`
- Broad “everything” sweep (expect many hits, tests ordering):  
  `tb search "update" --profile base_config --limit 0`

## Background refresh pattern
- Use systemd timer (see README) to run:  
  `tb mail fetch --profile base_config --sync --prune --full-rescan`
- For ad-hoc refreshes (manual):  
  `tb mail fetch --profile base_config --sync` (incremental)

## Compose (read-only guardrails)
- Open composer for review:  
  `tb mail compose --to a@b --subject "Check-in" --body "text"`
- Send without opening (only when intentional):  
  `tb mail compose --to a@b --subject "Send now" --body "text" --send`

## Safety reminders
- Searches read Postgres only; `--refresh` or `tb mail fetch` are the only paths that touch mbox files.
- Avoid `--prune` unless you need strict DB mirroring of what Thunderbird has on disk.
- Prefer `--account` + date bounds when narrowing recent evidence.
