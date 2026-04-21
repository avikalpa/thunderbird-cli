---
name: thunderbird-cli
description: Use when you need to inspect Thunderbird or Betterbird mail data from the local machine, especially to list profiles, hydrate the local cache, search mail, read matching messages, or trigger sync/send through the thunderbird-cli repository.
---

# Thunderbird CLI

Use this skill for local mail investigation through `/home/user/gh/thunderbird-cli`.

## What this tool does

`tb` reads existing Thunderbird/Betterbird profiles, builds a fast local cache, searches that cache, reads matching messages from the real mail stores, and can send through configured identities when the current build/runtime support it.

## Safety rules

- Treat Thunderbird/Betterbird profile data as read-only.
- Do not modify mbox, `.msf`, Thunderbird SQLite files, or prefs files.
- Writes are limited to the configured cache backend, temporary isolated send-profile clones, and the optional legacy JSON index.
- Prefer explicit `--profile` when more than one profile exists.
- Start on unfamiliar machines with `tb doctor`.
- When sending mail, prefer `tb mail compose --send --open=false` over ad hoc SMTP.
- Verify automated send in Sent/INBOX/Junk evidence, not SMTP success alone.
- Prefer `tb mail authcheck` over hand-written IMAP polling when you need receiver-side placement or authentication evidence; it now tolerates provider IMAP search misses by scanning recent mailbox headers directly.

## Repository and build

- Repo: `/home/user/gh/thunderbird-cli`
- Build: `go build -o bin/tb ./...`
- Smoke test: `./tests/run.sh`

## Backend model

Default backend:

- SQLite
- path under `~/.local/state/thunderbird-cli/...` unless overridden

Optional backend:

- PostgreSQL via `TB_STORE=postgres` and `TB_PG_DSN=...`

Inspect the live environment:

```sh
/home/user/gh/thunderbird-cli/bin/tb doctor
/home/user/gh/thunderbird-cli/bin/tb features
```

## Profile detection

`tb` checks profile roots in this order:

1. `THUNDERBIRD_HOME`
2. `~/.thunderbird`
3. `~/.var/app/eu.betterbird.Betterbird/.thunderbird`
4. `~/.var/app/org.mozilla.Thunderbird/.thunderbird`

## Common workflow

1. Inspect the machine:
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb doctor
   ```
2. List profiles:
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb mail profiles
   ```
3. Hydrate or refresh the default SQLite cache:
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb mail fetch --profile <profile> --sync
   ```
4. Search indexed mail:
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb search "keyword" --profile <profile> --limit 50
   ```
5. Read matching messages:
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb mail show --profile <profile> --folder INBOX --query "text" --limit 1 --thread
   ```
6. Send through `tb` when you need an auditable trail:
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb mail compose --profile <profile> --to someone@example.org --subject "Subject" --body "Body"
   /home/user/gh/thunderbird-cli/bin/tb mail compose --profile <profile> --from someone@example.org --to recipient@example.org --subject "Subject" --body "Body" --send --open=false
   ```

## Fast commands

For simple operator work and agent prompts, prefer the short top-level commands:

```sh
/home/user/gh/thunderbird-cli/bin/tb list INBOX --account <account@example.org> --limit 20 --raw
/home/user/gh/thunderbird-cli/bin/tb tail --account <account@example.org> --limit 30 --raw --ignore-folder junk,trash
/home/user/gh/thunderbird-cli/bin/tb head --account <account@example.org> --limit 10 --raw
/home/user/gh/thunderbird-cli/bin/tb read --message-id '<message-id>'
/home/user/gh/thunderbird-cli/bin/tb find --account <account@example.org> --since 2026-01-01 --raw "keyword"
/home/user/gh/thunderbird-cli/bin/tb mail sentcheck --profile <profile> --from <account@example.org> --subject "Subject"
/home/user/gh/thunderbird-cli/bin/tb mail move --profile <profile> --account <account@example.org> --source-mailbox Junk --dest-mailbox INBOX --message-id '<message-id>'
```

Mapping:

- `tb list ...` -> `tb mail recent ...`
- `tb tail ...` -> `tb mail unified ...`
- `tb head ...` -> `tb mail unified --oldest ...`
- `tb read ...` -> `tb mail show ...`
- `tb find ...` -> `tb mail search ...`
- `tb search ...` still works and maps to `tb mail search ...`

## Time-sensitive mailbox triage

Do not start a live incident hunt with a narrow keyword unless you already know the exact sender, subject, or failure string. Human replies often come from a different address and subject than the automated mail that started the thread.

Preferred workflow:

1. Sync first when freshness matters:
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb mail fetch --profile <profile> --sync
   ```
2. Tail the newest inbox mail before searching:
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb tail --profile <profile> --account <account@example.org> --limit 30 --raw --ignore-folder junk,trash
   ```
3. Check junk with the same pattern:
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb list "Junk Mail" --profile <profile> --account <account@example.org> --limit 20 --raw
   ```
4. Once the correct message is visible, inspect it directly by Message-ID:
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb read --profile <profile> --message-id '<message-id-from-recent>'
   ```
5. Only after that, narrow with cache search if you need the surrounding thread or historical context:
   Note: if you use `--since` / `--till` and get no results, newer builds of `tb` will tell you when matching mail exists outside the requested date window.
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb find --profile <profile> --account <account@example.org> --since 2026-01-01 --raw "keyword"
   ```

Anonymized example:

- A maintainer-account workflow looked blocked because `tb search "support@service.example"` only showed old automated mail.
- `tb tail --account ops@example.org --limit 30 --raw --ignore-folder junk,trash` exposed a new human reply from `person@project.example` with a different subject.
- `tb read --message-id '<...>'` revealed the real next-step link.

That is the default investigation path for mailbox triage now. Search comes after recent-message inspection, not before it.

## Operational notes

- `--sync` uses `THUNDERBIRD_BIN` if set; otherwise it falls back to `betterbird`, `thunderbird`, or `flatpak run <THUNDERBIRD_FLATPAK_ID>`.
- When the detected client is a Flatpak Betterbird/Thunderbird install and a real GUI session is available, `--sync` now reuses the already-running app instead of failing just because Betterbird is open.
- Google, Microsoft, and Yahoo identities can use the direct OAuth send path when the current build includes it and the runtime supports it.
- Standard SMTP/IMAP accounts with stored encrypted passwords can also use the direct headless send path when NSS-backed secret decryption is available.
- If direct send is unavailable, `tb` falls back to isolated Betterbird automation for unsupported cases.
- For machine-readable output, prefer `--raw` where available.
- For mailbox triage, prefer `tb mail recent ... --raw` over guessing the right search term too early.
- SQLite is the default because it is the lowest-friction portable search backend.
- Switch to PostgreSQL only when you actually need it.

## README contract

- Keep install and update at the top of `README.md`.
- Keep examples realistic enough that an operator can paste them.
- Keep `README.md` and `CHANGELOG.md` aligned when behavior changes.
- Remember that docs are user-facing product surface, not filler.
