---
name: thunderbird-cli
description: Use when you need to inspect Thunderbird or Betterbird mail data from the local machine, especially to list profiles, hydrate a Postgres mail index, search mail, read matching messages, or trigger a sync through the thunderbird-cli repository.
---

# Thunderbird CLI

Use this skill for local mail investigation through `/home/user/gh/thunderbird-cli`.

## What this tool does

`tb` reads existing Thunderbird/Betterbird profiles, ingests mail metadata and bodies into Postgres, and searches the cached index. Thunderbird/Betterbird remains the source of truth.

## Safety rules

- Treat Thunderbird/Betterbird profile data as read-only.
- Do not modify mbox, `.msf`, SQLite, or prefs files.
- Writes are limited to Postgres and the optional legacy JSON cache.
- Prefer explicit `--profile` when more than one profile exists.
- When sending mail, prefer `tb mail compose --send --open=false` over ad hoc SMTP. For supported providers, `tb` reuses the Betterbird profile's stored auth material and appends the exact message to Sent over IMAP; for unsupported providers it falls back to isolated Betterbird automation.

## Repository and build

- Repo: `/home/user/gh/thunderbird-cli`
- Build: `go build -o bin/tb ./...`
- Smoke test: `./tests/run.sh`

## Profile detection

`tb` checks profile roots in this order:

1. `THUNDERBIRD_HOME`
2. `~/.thunderbird`
3. `~/.var/app/eu.betterbird.Betterbird/.thunderbird`
4. `~/.var/app/org.mozilla.Thunderbird/.thunderbird`

If Betterbird is installed by Flatpak, the usual root is `~/.var/app/eu.betterbird.Betterbird/.thunderbird`.

## Common workflow

1. List profiles:
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb mail profiles
   ```
2. Hydrate or refresh Postgres from a profile:
   ```sh
   TB_PG_DSN=postgres://user:pass@localhost/dbname \
   /home/user/gh/thunderbird-cli/bin/tb mail fetch --profile <profile> --sync
   ```
3. Search indexed mail:
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb search "keyword" --profile <profile> --limit 50
   ```
4. Read matching messages:
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb mail show --folder <folder> --query "text" --limit 1 --thread
   ```
5. Compose or send through `tb` when you need an auditable trail:
   ```sh
   /home/user/gh/thunderbird-cli/bin/tb mail compose --to someone@example.org --subject "Subject" --body "Body"
   /home/user/gh/thunderbird-cli/bin/tb mail compose --profile base_config --to someone@example.org --subject "Subject" --body "Body" --send --open=false
   /home/user/gh/thunderbird-cli/bin/tb mail compose --profile base_config --from avikalpakundu@gmail.com --to someone@example.org --subject "Subject" --body "Body" --send --open=false
   ```

## Operational notes

- `--sync` will use `THUNDERBIRD_BIN` if set; otherwise it falls back to `betterbird`, `thunderbird`, or `flatpak run <THUNDERBIRD_FLATPAK_ID>`.
- `compose/send` accepts `--from` to choose the Thunderbird/Betterbird identity explicitly when multiple accounts exist.
- `compose --send --open=false` now prefers a direct provider-aware path. Google identities are sent headlessly with stored Betterbird OAuth tokens plus SMTP/IMAP XOAUTH2, and the exact message is appended to the configured Sent folder.
- Thunderbird's CLI path still only handles `-compose`, not a true top-level `-send`; unsupported providers therefore fall back to an isolated temporary clone of the chosen profile plus `Xvfb`/`xdotool` automation.
- For machine-readable output, prefer `--raw` where available.
- If no mail has been fetched into Postgres yet, `tb search` can self-hydrate once.
- If the profile exists but has no configured account data, `tb` can still list profiles but searches and folder reads will be empty.
- After any automated send, verify the result in the live mailbox, not just SMTP success:
  - check `Sent Items` in Betterbird when the message should be user-auditable
  - check `INBOX` and `Junk Mail` for delivery status notifications such as `Mail delivery failed: returning message to sender`
  - prefer direct IMAP verification over local offline cache when timing matters
  - if the GUI Betterbird instance already holds the live profile open, a second headless sync on that same profile will fail with a profile-lock error; do not treat that as send failure
  - local `.msf`/offline cache updates can lag behind a successful direct SMTP+IMAP send until the running GUI instance decides to sync that account
  - forwarded support aliases can still bounce due to SPF at a downstream recipient even when the original submission succeeded; that is a recipient-side forwarding problem, not proof that the local send failed

## When to read more

- Read `README.md` for full CLI usage and systemd examples.
- Read `AGENTS.md` for repo-specific operational guidance.
