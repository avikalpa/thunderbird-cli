# thunderbird-cli (`tb`)

Postgres-backed CLI for safely reading, searching, and composing mail using existing Thunderbird/Betterbird profiles. Thunderbird remains the source of truth; `tb` ingests mbox files into Postgres and queries the database (no direct mbox scanning during searches once hydrated).

## Requirements
- Go 1.21+
- Thunderbird/Betterbird profile under `~/.thunderbird` (override with `THUNDERBIRD_HOME`)
- Postgres available via `TB_PG_DSN` (e.g. `postgres://user:pass@localhost/dbname`). Schema is created automatically (`tb_messages`, `tb_meta`).
- Optional: place `TB_PG_DSN=...` in a local `.env` (ignored by git); see `.env.example` for the format.

## Build
```sh
cd ~/git/thunderbird-cli
go build -o bin/tb ./...
```

## Recommended workflow
1) **Fetch + ingest to Postgres (read-only)**  
   ```sh
   TB_PG_DSN=postgres://... tb mail fetch --profile base_config --sync
   ```
   - Default is incremental ingest (skips unchanged folders). `--full` forces a full rescan; `--prune` implies full. `--sync` runs headless Thunderbird/Betterbird (or `flatpak run <THUNDERBIRD_FLATPAK_ID>`, default `eu.betterbird.Betterbird`) first.
   - Optional filters: `--account/--ac <email>`, `--folder <substring>`, `--max-messages N`, `--tail N`.

2) **Search from Postgres (no mbox reads)**  
   ```sh
   tb search "invoice" --profile base_config --limit 50
   tb search "meeting" --profile base_config --account user@example.com --since 2024-01-01 --till 2024-06-30
   ```
   - Options: `--account/--ac`, `--folder` (optional narrow), `--since/--ds YYYY-MM-DD`, `--till/--dt YYYY-MM-DD`, `--limit N`, `--refresh` (incremental ingest before searching), `--full-rescan` (force full rebuild before searching), `--raw` (plain lines for LLMs), `--fuzzy` (token AND).
   - Shortcut: `tb search ...` == `tb mail search ...`.
   - If the Postgres cache for the profile is empty, `tb search` will ingest once automatically (full scan).

3) **Inspect full messages**  
   ```sh
   tb mail show --folder ImapMail/example.com/INBOX --query "subject fragment" --limit 1 --thread
   ```

4) **Compose**  
   ```sh
   tb mail compose --to a@b --subject "Update" --body "text"   # opens composer
   tb mail compose --to a@b --subject "Send now" --body "text" --send
   ```

## Commands (summary)
- `tb mail profiles` — list Thunderbird profiles.
- `tb mail folders --profile <name>` — list mbox folders/sizes.
- `tb mail fetch [--profile p] [--sync] [--prune] [--full] [--account/--ac email] [--folder f] [--max-messages N] [--tail N]` — ingest mail into Postgres (incremental by default; add `--full` for a full rebuild, implied when `--prune` is set).
- `tb search ...` — search Postgres cache.
- `tb mail show/read --folder <name> --query "<text>" [--limit N] [--thread]` — print full message(s).
- `tb mail compose/send ...` — open/send via Thunderbird composer.
- `tb mail index ...` — legacy JSON cache (Postgres is the primary store).

Note: the first refresh after enabling the fingerprinted incremental flow may perform a full scan to seed fingerprints; subsequent `--refresh` runs skip unchanged folders.

## Systemd timer example (hourly fetch)
`~/.config/systemd/user/tb-fetch.service`:
```
[Unit]
Description=tb mail fetch (profile base_config)

[Service]
Type=oneshot
Environment=TB_PG_DSN=postgres://user:pass@localhost/dbname
Environment=THUNDERBIRD_HOME=%h/.thunderbird
ExecStart=%h/git/thunderbird-cli/bin/tb mail fetch --profile base_config --sync --prune --full
```

`~/.config/systemd/user/tb-fetch.timer`:
```
[Unit]
Description=Run tb mail fetch hourly

[Timer]
OnCalendar=hourly
Persistent=true

[Install]
WantedBy=timers.target
```

Enable with:
```sh
systemctl --user daemon-reload
systemctl --user enable --now tb-fetch.timer
```

## Safety
- Read-only against Thunderbird data; we never mutate mbox, `.msf`, or prefs. Writes happen only to Postgres and the optional legacy `.tb-index.json`.
- `--prune` is destructive to the database (removes rows for the profile not seen in the current scan); leave it off unless you want strict mirroring. `--prune` implies a full rescan.
- No folder argument is required—searches span all folders by default; use `--account` and date bounds to narrow.
- Thunderbird GUI remains the owner for account setup and any risky operations (send, folder moves, deletes).

## Tests
```sh
./tests/run.sh   # requires TB_PG_DSN for integration fetch/search; otherwise runs go test + build
```

## Paths & binaries
- Thunderbird root: `~/.thunderbird` by default; override with `THUNDERBIRD_HOME`.
- Binary overrides: `THUNDERBIRD_BIN` (direct path), `THUNDERBIRD_FLATPAK_ID` (Flatpak ID; default `eu.betterbird.Betterbird`).
- Preferred binary name/location: `bin/tb` (git-ignored).

## License
Apache License 2.0 — Copyright 2025 Avikalpa Kundu.
