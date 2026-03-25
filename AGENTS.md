# Agent Notes

- **Mission:** act on email evidence quickly via CLI. Thunderbird/Betterbird remains the owner of profiles; `tb` only reads mbox data and writes to Postgres.
- **Safety:** treat Thunderbird data as read-only. We never mutate the live profile's mbox, `.msf`, SQLite, or prefs. Writes go to Postgres (`tb_messages`, `tb_meta`), temporary isolated send-profile clones, and the legacy `.tb-index.json` only if `tb mail index` is used.
- **Profiles:** `tb mail profiles` to choose; prefer explicit `--profile` when searching/fetching.
- **Primary workflow:** hydrate Postgres with `tb mail fetch --profile <p> --sync` (optional `--account/--ac`, `--folder`; `--full` forces full rescan; `--prune` implies full). Searches run **only** against Postgres; `tb search ...` spans all folders by default, self-hydrates once if the cache is empty, and can be refreshed incrementally with `--refresh` (or fully with `--full-rescan`).
- **Narrowing:** prefer `--account` and date bounds (`--since/--ds`, `--till/--dt`) instead of forcing folder names. Folder filter is optional and fuzzy.
- **Reading full messages:** after a hit, use `tb mail show --folder <match> --query "<subject/body fragment>" [--limit N] [--thread]` to print bodies. Use table output for humans, `--no-fancy` for machines.
- **Sync path:** `--sync` uses `THUNDERBIRD_BIN` if set, otherwise `betterbird`/`thunderbird`, or `flatpak run <THUNDERBIRD_FLATPAK_ID>` (default `eu.betterbird.Betterbird`). GUI remains the last resort for risky ops.
- **Send path:** for headless sends, use `tb mail compose --profile <p> --send --open=false ...`. Google, Microsoft, and Yahoo identities are handled directly from the Betterbird/Thunderbird profile: NSS-decrypt the stored refresh token, refresh an access token, send over SMTP XOAUTH2, then append to Sent over IMAP XOAUTH2. Unsupported providers still fall back to the isolated temp-profile + `Xvfb`/`xdotool` path because Thunderbird's command-line handler only guarantees compose-window creation.
- **Caching:** Postgres is the canonical cache. `--prune` deletes rows for the profile that were not seen in the current scan—leave it off unless strict mirroring is desired.

## README Section

- Treat `README.md` as the operator manual, product pitch, and first demo of software quality.
- Keep fast install and fast update instructions at the very top.
- Prefer examples that look like real operator work, not toy one-liners.
- When a feature materially improves user workflow, update `README.md` and `CHANGELOG.md` in the same change.
- Treat `CHANGELOG.md` as the second marketing surface: write visible user wins, concrete commands, and behavior changes that matter in practice.

## Operational tips
- Run `tb mail fetch --sync` before time-sensitive hunts; automate with the systemd timer in README for hourly refreshes (incremental).
- When searching “just arrived” mail without a timer, add `--refresh` (incremental); reserve `--full-rescan` for integrity checks or prune operations.
- Expect the first refresh after upgrading to the incremental flow to run a full scan to seed fingerprints; subsequent refreshes will skip unchanged folders.
- CLI shortcuts: `tb search "text"` (table, bold headers by default), `--raw` for LLM-friendly lines, `tb read --folder ... --query ...` to dump bodies, `tb send` as an alias for compose.
- Release hygiene: do not publish binaries locally. Use GitHub Actions to build and attach release artifacts for all platforms/arches; keep local builds for testing only.
- Skip folder args unless absolutely necessary; start wide, then add `--account` and dates to narrow noise (Spam/Junk included automatically).
- If a search is unexpectedly empty, check whether Postgres is hydrated (`tb search` will auto-hydrate once) and consider `--refresh` after GUI fetch.
- If automated send succeeds locally but the recipient forwards mail onward, inspect DSNs in `INBOX`/`Junk Mail`; downstream SPF failures are common and do not imply the original submission failed.
- If Betterbird already has the target profile open, a second headless sync against that same profile can fail on the profile lock. That blocks immediate local-cache verification, but it does not invalidate a successful direct SMTP+IMAP send.

## TODOs
- Detect staleness via `tb_meta` and auto-refresh when last scan is older than a configurable window.
- Incremental fetch (mtime/size checks) to avoid full rescans on large profiles.
- Improve search relevance (Postgres tsvector/trigram) and optional JSON output.
- Thread traversal via In-Reply-To/References across folders.
- Attachment/text extraction helpers with size guards.

## Nice to have
- TUI browser for folders/results.
- Saved searches and named filters.
- Pluggable cache backends (SQLite) with integrity checks.
- Performance telemetry during fetch/search (counts, timings) saved to `tb_meta`.
