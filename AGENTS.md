# Agent Notes

- Mission: act on Thunderbird or Betterbird mail quickly through a deterministic CLI.
- Safety: treat Thunderbird data as read-only. `tb` does not rewrite live mbox, `.msf`, Thunderbird SQLite files, or `prefs.js` during normal search work.
- Writes go to the configured cache backend, temporary isolated send-profile clones when fallback send is needed, and the optional legacy `.tb-index.json` only if `tb mail index` is used.
- Default backend: SQLite. Optional backend: PostgreSQL via `TB_STORE=postgres` and `TB_PG_DSN=...`.
- Profile discovery prefers `THUNDERBIRD_HOME`, then Flatpak Betterbird/Thunderbird profile roots, then `~/.thunderbird`.
- First command on an unfamiliar machine: `tb doctor`.
- First command before a time-sensitive hunt: `tb mail fetch --profile <p> --sync`.
- First inspection during a time-sensitive hunt: `tb tail --profile <p> --account <acct> --limit 30 --raw --ignore-folder junk,trash`, then check `Junk Mail` separately if needed.
- **Start with `tb q "<what you are looking for>"`.** It searches every account and folder, refreshes a stale cache, ranks by relevance, widens automatically when nothing matches, and emits JSON when piped. Reach for `search`/`find` only when you need a flag `q` does not expose.
- Every `q` result carries a `read` field holding the exact next command. Use it verbatim rather than composing `--folder`/`--query` by hand.
- Prefer one enriched `q` call over several plain ones: `--body` returns bodies inline (no follow-up read), `--thread` returns the whole conversation, `--important` ranks by direct/thread/deadline signals, and `--since/--till` take `today`, `7d`, `2019-07`. "What came in today?" is `tb q --today --important`.
- `--important` is a heuristic and prints `importance_why`; quote those reasons rather than presenting the ranking as fact.
- Preferred search output for machine consumption: `tb q ...` (JSON) or `tb search --raw ...`.
- Preferred exact-read follow-up after recent-mail triage: `tb read --message-id '<...>'`.
- A negative result is only evidence when the tool says what it searched. `tb` names the folders it read on an empty result and lists candidates for an unknown `--folder`; quote those in any "no such mail" conclusion.
- `--sync` fails rather than degrading to the stale cache. Check `tb doctor`'s `Sync display path` before a time-sensitive hunt; over plain ssh `tb` joins a running Betterbird/Thunderbird session automatically.
- Never conclude a send failed from a local Sent-folder read; the local mbox cache lags. Use `--verify` at send time or `tb mail sentcheck` afterwards. Re-sending off a stale read has already produced a duplicate to a live support ticket.
- Replies to a ticket or thread need `--in-reply-to` (and `--references` when you have the chain). Without them the receiving desk usually opens a new ticket.
- Use `--body-file` for anything longer than a line; `--body` over ssh is quoting hell.
- A sync that changes nothing is reported, not assumed. If `--sync` reports it did not modify the mail store, the client most likely never opened the profile — check for a stale `<profile>/lock` and run the mail binary with `-profile <path>` on a real display to see what dialog it is waiting on.
- Select profiles with `-profile <absolute path>`, never `-P <name>`: an unbindable name silently opens the graphical profile chooser, which on a headless host waits forever on an invisible display.
- Ingest resumes from where it stopped when a folder only grew. `--full` and `--prune` still scan everything; use them when the cache must be authoritative.
- Before trusting any `tb` behaviour on a machine, check `tb doctor`'s `Installed binaries:` block — a second copy on a different `PATH` entry is the usual reason a fix "did not take".
- Preferred narrowing dimensions: `--account`, `--since`, `--till`. Use `--folder` only when it materially reduces noise.
- Direct provider-aware headless send currently supports Google, Microsoft, and Yahoo identities stored in the selected Thunderbird or Betterbird profile.
- If direct send is unavailable in the current build, `tb features` and `tb doctor` are the authority. Do not guess.
- If an automated send succeeds locally but a recipient later forwards mail onward, inspect DSNs in `INBOX` and `Junk Mail`. Downstream SPF failures do not mean the original local submission failed.

## README Section

- Treat `README.md` as the operator manual, product pitch, distribution guide, and first proof of software quality.
- Keep the fastest install and fastest update instructions at the very top.
- Prefer real operator examples over toy snippets.
- Make the README credible for first-time users, not just existing contributors.
- When a change materially improves workflow, update `README.md` and `CHANGELOG.md` in the same change.
- Treat `CHANGELOG.md` as the second marketing surface: concrete wins, real commands, and behavior that matters in practice.
- Release pages should reuse the relevant curated changelog entry rather than autogenerated notes whenever the forge or hosting platform supports custom release bodies.
- Document licensing clearly. Code is Apache-2.0. Markdown documentation is CC BY-SA 4.0.

## Operational tips

- `tb doctor` is the quickest way to understand profile detection, backend selection, and runtime blockers.
- During mailbox triage, do not trust a guessed sender or subject too early. Recent-mail inspection comes before keyword search.
- SQLite is the default because local single-user search matters more than service setup friction.
- PostgreSQL remains valuable, but it should be opt-in rather than a gate on first-run use.
- Release/install work is part of product quality. Keep `install.sh`, `tb update`, and release packaging in sync.
- Do not over-promise portability. Use `tb features` and `tb doctor` to report what the current build can actually do.
- Release archives should include the binary, README, and license files.

## README And Release Notes Contract

- `README.md` should stay brutally practical: open with install, `tb doctor`, and update flow, then walk through real operator jobs such as fetch, search, read, and headless send.
- Keep examples realistic enough that someone debugging a live mailbox can paste them without translation.
- `CHANGELOG.md` should preserve validated operator-facing wins with the exact commands or workflows that prove them.
- GitHub release pages should use curated notes from `CHANGELOG.md`, generated through `scripts/release-notes.sh`, not autogenerated summaries.
- Keep `README.md`, `CHANGELOG.md`, installer/update behavior, and release artifacts aligned.

## Release Notes Automation

- Keep the canonical changelog current with `## Unreleased` at the top while work is in flight.
- When cutting a release, move the user-visible notes into an exact version section before or during the tag workstream.
- Release automation should prefer the exact version section and fall back to `Unreleased` so curated notes still publish when the rename is late.
- Release pages should use curated changelog text rather than autogenerated notes.
