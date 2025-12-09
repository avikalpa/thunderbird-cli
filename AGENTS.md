# Agent Notes

- Mission: act on email evidence quickly without relying on the Thunderbird GUI unless absolutely necessary; use the CLI to list, search, and summarize mail so users open the GUI only for account management.
- Safety: treat Thunderbird data as **read-only**. Do not edit/delete mbox files, `.msf`, `.sqlite`, or prefs. The only write the CLI performs is the optional cache `.tb-index.json` in a profile. Composing/sending goes through Thunderbird itself.
- Profiles: use `tb mail profiles` to pick a profile; `tb mail folders --profile <name>` to discover folders. Prefer explicit `--profile` to avoid touching the wrong account.
- Reading/searching: `tb mail recent <folder> [--limit N] [--query text]` for quick previews; `tb search "<query>" [--since/--ds] [--till/--dt] [--account/--ac email] [--max-messages] [--raw] [--no-fancy] [--no-index]` for deeper scans. Default output is a table with snippets; `--no-fancy` gives plain lines for machine consumption. `--raw` uses ripgrep for fast text hits.
- Indexing/cache: `tb mail index ...` writes `.tb-index.json` for faster repeated searches. Delete it to drop the cache; add `--no-index` to bypass it temporarily. The cache auto-refreshes when stale or incomplete.
- Sending: `tb mail compose ...` opens Thunderbird’s composer. Only add `--send` when auto-send is desired; otherwise default to opening for review.
- Workflow: keep commands reproducible, avoid actions that mutate profile data, and prefer explicit filters (profile/account/folder/date) when narrowing evidence.

## TODOs
- Add richer threading support (walk In-Reply-To/References, multi-folder threads).
- Improve fuzzy search (token + regex) and expose saved searches.
- Add message export (JSON/mbox slice) for downstream tools.
- Harden date parsing with additional legacy formats and timezone edge cases.
- Build optional attachment/text extraction helpers with size guards.
- Add configurable fetch helper (env `THUNDERBIRD_BIN`) and detection of install paths.

## Nice to have
- Interactive TUI wrapper for browsing folders/results.
- Configurable output themes (table widths, JSON output).
- Pluggable cache backends (SQLite) with integrity checks.
- Optional parallel search scheduler for very large profiles.
