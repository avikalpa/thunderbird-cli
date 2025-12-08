# thunderbird-cli

Go CLI to browse, search, and compose mail using existing Thunderbird profiles. It reads mailboxes directly from `~/.thunderbird` (or `THUNDERBIRD_HOME`) and uses Thunderbird itself for composing/sending so all existing account settings continue to work.

## Build
```sh
cd ~/git/thunderbird-cli
go build -o bin/tb ./...
```

## Usage
- `tb mail profiles` — list profiles from `profiles.ini`.
- `tb mail folders [--profile <name>]` — list mbox files under `Mail/` and `ImapMail/`.
- `tb mail recent <folder> [--profile <name>] [--limit 20] [--query text]` — show latest messages from a folder (query filters subject/from/body).
- `tb mail search "<query>" [--folder Inbox] [--profile <name>] [--limit 25] [--since/--ds YYYY-MM-DD] [--till/--dt YYYY-MM-DD] [--account/--ac <email>] [--max-messages N|--all] [--raw] [--no-fancy] [--no-index]`  
  - Default mode parses MIME/HTML and shows a table, newest-first.  
  - `--since/--ds` and `--till/--dt` filter by Date header.  
  - `--account/--ac` restricts to an account’s mailboxes (by identity email from prefs.js).  
  - `--max-messages` caps messages scanned per folder (0 = all; scans from start); `--all` disables any cap.
  - `--raw` uses ripgrep for fast text hits; `--no-fancy` gives plain lines (LLM-friendly).
  - `--no-index` forces live mbox scanning even if a cache exists.
- `tb mail index [--profile p] [--folder f] [--account/--ac email]` — prebuild the search cache for faster repeated queries.
- Shortcut: `tb search ...` is equivalent to `tb mail search ...`.
- `tb mail compose --to a@b --subject "Hi" --body "text" [--cc ...] [--send]` — launch Thunderbird composer (adds `-send` if you set `--send`).

Notes:
- Default Thunderbird root is `~/.thunderbird`; override with `THUNDERBIRD_HOME`.
- Search auto-refreshes the cache if it is missing, stale, or empty; falls back to live scans.
- Folder matching is fuzzy: `Inbox` matches `Mail/Local Folders/Inbox`. Use `tb mail folders` to see exact names.
- Searches read mbox files directly; first runs on big folders take longer. Use `tb mail index ...` to cache for speed.
- Read-only by design: the only write we perform is an optional cache file `.tb-index.json` in the profile directory. We never mutate mbox/config; sending uses Thunderbird itself.
- `--send` relies on Thunderbird’s `-compose ... -send` support; if it fails, drop `--send` to open the composer window instead.

### Binary placement
- Preferred binary name: `tb` (build to `bin/tb`).
- `.gitignore` ignores `bin/`, `tb`, and `thunderbird-cli` to avoid checking binaries into git.

## Examples
- List folders for a profile: `tb mail folders --profile myprofile`
- Search all mail for a keyword: `tb search --profile myprofile --limit 0 "invoice"`
- Narrow to an account: `tb search --profile myprofile --account user@example.com --limit 0 "tax"`
- Date-bounded search: `tb search --profile myprofile --since 2024-01-01 --till 2024-12-31 "contract"`
- Folder-specific search: `tb search --profile myprofile --folder ImapMail/mail.example.com/INBOX --limit 0 "provider name"`
- Build cache then search:  
  `tb mail index --profile myprofile --tail 0`  
  `tb search --profile myprofile --limit 0 "keyword"`
- Fast text grep: `tb search --profile myprofile --raw --limit 50 "keyword" --no-fancy`

## Tests
- Quick check: `./tests/run.sh`  
  Always runs `go test ./...`, builds `bin/tb`, and prints `tb help`. If a Thunderbird profile is available, it will also run a tiny search against the first profile (or `TB_PROFILE`).

## Safety
- Read-only for mailboxes/config; the only write is `.tb-index.json` (optional cache). Delete it to drop the cache; use `--no-index` to bypass it temporarily.
- Composing/sending goes through Thunderbird; prefer interactive compose unless auto-send is intentional.

## License
Apache License 2.0 — Copyright 2025 Avikalpa Kundu.

## Logging & drills
- Logs live under `logs/` (gitignored). Scripts emit start/end/duration metadata alongside results.
- Broad search drills: `./scripts/smoke_parallel.sh` (env: `BIN`, `PROFILE`, `ACCOUNT`, `LOG_DIR`). Runs `cfa` (account + all), `cesc`, `bill`, and `court*` searches in parallel; outputs tables by default.
- Timeline search helper: `./scripts/search_timeline.sh "query" folder_path` (env: `PROFILE`, `BIN`, `LOG_DIR`, `LIMIT`, `TAIL`, `NO_INDEX`).
