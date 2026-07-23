# Changelog

All notable user-visible changes to `thunderbird-cli` are recorded here.

This file is the second product surface after the README. It should tell a serious operator what got faster, safer, or easier, and show the command that proves it.

## [Unreleased]

- No user-visible changes yet.

## [3.2.0] - 2026-07-23

Incremental ingest, plus the fixes found by making a headless LXC container the
primary mail host. Those fixes are all the same defect class as 3.1.0: a step
that failed while reporting success.

### Ingest resumes instead of re-reading

mbox files grow by appending, but every ingest re-read each changed folder from
byte zero. A 118MB INBOX that gained one message cost 118MB of parsing.

`tb` now records how far it ingested and resumes there, after proving the
previous end still lands on an mbox message separator. A rewritten or compacted
folder fails that check and falls back to a full scan, so mail is never skipped.

```
$ tb mail fetch --account ops@example.org
info: ingest parsed 1.0KiB of 148.1MiB (resumed where the previous ingest stopped)
```

Measured on a real 148MB account: **4.2s -> 0.021s**. `--full` and `--prune` still
scan everything, since they rebuild the complete message set.

### `--sync` no longer claims success when it changed nothing

- `tb` now fingerprints the mail store before and after a sync. If nothing changed on a timeout, it **fails** with the likely cause instead of printing `assuming mail client had enough time to fetch` and returning 0.
- when a sync completes cleanly and legitimately had nothing to fetch, it says so:

  ```
  info: sync completed without changing the mail store (no new mail, or nothing to fetch)
  ```

### Profiles are selected by path, not by name

- `tb` passed `-P <name>` to the mail client. When the name does not bind — a `profiles.ini` entry whose directory is missing is enough — Thunderbird/Betterbird silently opens the graphical **"Choose User Profile"** dialog. On a headless host that dialog waits forever on an invisible display while `tb` reports success.
- `tb` now passes `-profile <absolute path>`, which always binds. If a sync ever stalls again, the error tells you how to see the dialog it is waiting on.

### Virtual displays are verified, not assumed

- `startVirtualDisplay` hardcoded `:98` and treated the existence of `/tmp/.X11-unix/X98` as "Xvfb is ready". A leftover socket from a crashed Xvfb satisfied that instantly, so the mail client was handed a display number with no server behind it and died with `Exiting due to channel error`.
- `tb` now uses `Xvfb -displayfd`, letting Xvfb pick a free display and report the number it actually bound.

### Knowing which `tb` you are running

- `tb doctor` now lists every `tb` binary it can find — the running one, each one on `PATH`, and the conventional install directories — with versions:

  ```
  Installed binaries: 2
    3.2.0  /usr/local/bin/tb (on PATH)
    3.0.10  /home/user/.local/bin/tb
    WARNING: these copies are different versions.
  ```

- this matters because `tb update` only replaces the copy it is running from, `go build` leaves a binary in `./bin/tb`, and interactive vs. non-interactive shells often have different `PATH`s — so `ssh host 'tb ...'` and your terminal can run different builds.
- added `scripts/install-local.sh`: builds the working tree and installs it over the canonical path (`/usr/local/bin/tb`, override with `TB_INSTALL_DIR`).

## [3.1.0] - 2026-07-23

This release is about a single theme: **a command must never look like it worked
when it did not.** Every change below removes a way `tb` could return a
confident, wrong-looking answer.

### Sync no longer degrades silently

- `--sync` from a plain ssh shell used to fail with `Error: no DISPLAY environment variable specified` and then quietly answer from the stale cache. `tb` now discovers the GUI session of an already-running Betterbird/Thunderbird and joins it:

  ```sh
  $ tb mail sync --profile default
  info: no display in this shell; joining the running mail client (pid 2538978) (WAYLAND_DISPLAY=wayland-0)
  ```

- session values are validated against the host filesystem before use, so a Flatpak client's sandbox-internal `DBUS_SESSION_BUS_ADDRESS=unix:path=/run/flatpak/bus` is dropped rather than adopted and used to launch.
- when `--sync` genuinely cannot run, the command now fails instead of continuing against old cached mail. Drop `--sync` to search the cache on purpose.
- `tb doctor` reports the sync display path up front, so you know before you run a hunt:

  ```
  Sync display path: no display in this shell; will join the running mail client (pid 2538978) (WAYLAND_DISPLAY=wayland-0)
  ```

### Send now proves it sent

- headless send printed nothing at all on success, which made a real send indistinguishable from a no-op — and caused a duplicate mail to a support ticket. It now reports the evidence:

  ```sh
  $ tb mail compose --from ops@example.org --to support@example.com \
      --subject "Re: Ticket 13421571" --body-file reply.txt --send --open=false --verify 60s
  sent: ops@example.org -> support@example.com
    message-id: <1784801299325454332.2671297@example.com>
    transport:  smtp+password via mail.example.org:465
    sent copy:  appended to Sent Items
    verified:   Message-ID present in Sent Items
  ```

- `--verify <duration>` confirms the Message-ID from the **server**, not from the local mbox cache, which lags and is not evidence of a failed send.
- when a send succeeds but a later step fails, the error now carries the Message-ID and says not to re-send.

### Reply threading

- added `--in-reply-to` and `--references` to `tb mail compose`. Message-IDs are accepted with or without angle brackets, comma- or space-separated; `References` is derived from `--in-reply-to` when not given explicitly.
- the isolated-profile fallback cannot set these headers, so a threaded send through that path is **refused** rather than delivered unthreaded — an unthreaded reply silently opens a new ticket instead of appending to the existing one.
- `tb mail sentcheck` and `tb mail authcheck` now show `In-Reply-To` and `References`, so threading can actually be confirmed.

### Reading mail

- `tb mail recent` / `tb list` no longer require a folder positional; with none given they read the inbox(es):

  ```sh
  tb list --limit 20 --raw
  ```

- `--folder` now falls back to token matching, so half-remembered names resolve: `--folder "acme sent"` finds `ImapMail/mail.acme-1.example/Sent Items`.
- an unknown folder now lists what does exist instead of only saying `no folders match`.
- empty results name the folders actually read, so "no matches" can never be confused with "searched the wrong mailbox":

  ```
  No matches in 2 folder(s): ImapMail/imap.acme.example/Sent Items, ImapMail/mail.acme-1.example/Sent Items.
  ```

- a short `--folder INBOX` fans out across every account, and `tb` now says so rather than letting the first hit look authoritative.
- `--body-file <path>` reads a message body from a file (or `-` for stdin), so long bodies no longer have to survive shell quoting over ssh.

### Quieter, more honest warnings

- a single non-mbox file in the mail tree (typically Yahoo's `Trash`) emitted one `invalid mbox format` warning per record — 12,269 lines for one folder. Those are now summarised once:

  ```
  info: skipped 1 folder(s) that are not valid mbox files (set TB_VERBOSE=1 to list them)
  ```

- `TB_VERBOSE=1` lists one line per affected folder. Real errors are still reported, deduplicated per folder.

## [3.0.10] - 2026-05-18

### Sync robustness

- `tb` now prefers Flatpak Betterbird/Thunderbird profile roots before the legacy `~/.thunderbird` tree, preventing stale-host-profile reads when the active mail client is Flatpak Betterbird.
- added `tb mail sync --profile <p> --timeout 30s` so operators and agents can refresh mail without requiring a cache fetch.
- when `--sync` runs from a headless shell and `Xvfb` is available, `tb` starts a temporary virtual display instead of failing before Betterbird can fetch mail.
- `tb search --refresh` now syncs the profile before refreshing the cache, keeping just-arrived mail searches from using old local state.

## [3.0.9] - 2026-04-22

### Search robustness

- fixed `tb find` / `tb search` on cold caches so a first query no longer blocks behind an implicit full-profile ingest
- when a profile has no cached messages yet, search now falls back to scanning the real mailbox files directly and returns the hit immediately
- this keeps first-use operator lookups usable on large Betterbird or Thunderbird profiles while leaving full cache hydration as an explicit `fetch` / `--refresh` step

## [3.0.8] - 2026-04-21

### Mailbox training

- added `tb mail move` to move a real remote IMAP message between folders using an exact `Message-ID` or subject match
- this makes inbox-training workflows reproducible from the CLI, for example moving a message from Outlook `Junk` into `INBOX`

## [3.0.7] - 2026-04-21

### Authentication checks

- fixed `tb mail authcheck` for providers, especially Outlook, where IMAP header search can miss a freshly delivered message even though the message is already visible in the mailbox
- `authcheck` now falls back to scanning recent mailbox headers directly, so receiver-side placement and authentication evidence are still provable when server-side search is flaky

## [3.0.6] - 2026-04-14

### Search UX

- `tb find` and `tb search` now tell you when a query has matches outside the requested `--since` / `--till` date window instead of only printing `No matches.`
- this makes partial-memory lookups safer when the remembered term is right but the time filter is wrong

## [3.0.5] - 2026-04-13

### Sync behavior

- `tb mail fetch --sync` now reuses an already-running Flatpak Betterbird/Thunderbird GUI session instead of failing just because the mail client is open
- native Thunderbird/Betterbird binaries still use the existing headless sync path

## [3.0.4] - 2026-04-12

### Mailbox triage

- added `tb mail unified` plus the top-level aliases `tb tail` and `tb head`
- unified inbox views now support `--ignore-account` and `--ignore-folder` so operators can suppress noisy accounts or mailbox paths during live triage
- recent-mail hunting no longer requires guessing which single INBOX received the reply before you can inspect it by Message-ID

### Sent verification

- fixed `tb mail sentcheck` for servers where IMAP header search misses a just-sent message even though the message is present in the Sent mailbox
- `sentcheck` now falls back to scanning recent Sent headers directly, so the verification path matches what `tb list 'Sent Items' --sync` already proves

## [3.0.3] - 2026-04-11

### Simpler mailbox commands

- added three obvious top-level aliases for common mail operations:
  - `tb list ...` -> `tb mail recent ...`
  - `tb read ...` -> `tb mail show ...`
  - `tb find ...` -> `tb mail search ...`
- updated the README, playbook, skill notes, and agent notes to make `list -> read -> find` the default operator flow

### Sync robustness

- `tb mail fetch --sync` and `tb mail recent --sync` now fail fast when the detected mail client is a Flatpak Betterbird/Thunderbird install running from a headless shell with no real GUI session
- sync runs now respect `TB_SYNC_TIMEOUT` (default `90s`) instead of hanging indefinitely behind GUI runtime noise

### Authentication checks

- added `tb mail authcheck` to send a real message and poll the receiving account over IMAP for the delivered authentication headers
- useful for proving what Gmail, Yahoo, Outlook, or another real mailbox thought about SPF, DKIM, and DMARC instead of inferring from DNS alone

### Sent verification

- added `tb mail sentcheck` to verify a sent copy online via IMAP instead of trusting local cache state
- this makes headless send auditable even while Betterbird is running and the local Sent folder has not synced yet
- Microsoft / Outlook direct send now skips the extra IMAP Sent append, because Outlook already stores a Sent copy server-side and double-appending created duplicates

## [3.0.2] - 2026-03-26

### Mailbox triage

- made `tb mail recent` the right first step for fresh-mail investigations:
  - added `--account`
  - added `--raw`
  - added `--sync`
- added `tb mail show --message-id '<...>'` so agents and operators can open the exact mail discovered from a recent-message tail without guessing the subject again
- updated the README, playbook, skill, and agent notes to make the intended workflow explicit:

```bash
tb mail fetch --profile default --sync
tb mail recent INBOX --profile default --account user@example.org --limit 20 --raw
tb mail recent "Junk Mail" --profile default --account user@example.org --limit 20 --raw
tb mail show --profile default --message-id '<message-id-from-recent>'
```

## [3.0.1] - 2026-03-25

### Headless send

- added direct headless send for non-OAuth SMTP/IMAP accounts whose encrypted passwords are already stored in the Thunderbird or Betterbird profile
- `tb mail compose --send --open=false` no longer has to fall back to GUI automation for standard password-backed accounts like `mail.example.com`
- direct send still appends the exact RFC822 message to the real Sent folder after submission

### Capability reporting and docs

- `tb doctor`, `tb features`, README, SKILL, and playbook docs now describe secret-backed direct send more accurately instead of implying OAuth-only support
- runtime guidance now makes the NSS dependency story clearer for both OAuth and stored-password accounts

## [3.0.0] - 2026-03-25

`3.0.0` is the release where `tb` stopped requiring PostgreSQL for first-run usefulness and started acting like a real installable product instead of a promising local checkout.

### Headline changes

- SQLite is now the default cache backend.
- PostgreSQL is still supported, but only when explicitly selected.
- `tb doctor`, `tb features`, `tb version`, and `tb update` are first-class commands.
- release archives and `install.sh` now define the fast install/update path
- runtime capability reporting now explains whether direct OAuth send is compiled in and whether the current machine can actually use it

### Why this matters

Before `3.0.0`, the first serious question from a new user was usually some variant of:

- do I really need PostgreSQL just to search my own mail?
- why does this binary behave differently on different machines?
- how do I update this without rebuilding from source every time?

After `3.0.0`, the answers are much cleaner:

- no, SQLite is the default and works well for local mail operations
- the binary tells you the truth with `tb doctor` and `tb features`
- install and update are now designed as first-class operator commands

### Examples that are better now

Install and verify the machine in one short flow:

```bash
curl -fsSL https://raw.githubusercontent.com/avikalpa/thunderbird-cli/main/install.sh | sh
tb doctor
```

Refresh a profile and search without bringing PostgreSQL to the party:

```bash
tb mail fetch --profile default --sync
tb search "invoice" --profile default --limit 20
```

Inspect what the current build can actually do before trusting send automation:

```bash
tb features
tb doctor
```

Switch to PostgreSQL only when you actually want it:

```bash
export TB_STORE=postgres
export TB_PG_DSN='postgres://user:pass@localhost/tb_cli?sslmode=disable'
tb mail fetch --profile default --sync
tb search "release" --profile default
```

### Search and storage changes

- added a new SQLite cache backend using `FTS5`
- moved backend selection behind a store interface so SQLite can be the default and PostgreSQL can stay optional
- fixed the imported-profile account-directory issue by preferring usable profile-relative mail roots over stale absolute paths from another machine
- PostgreSQL search now uses the indexed full-text path instead of chained substring matches

### Runtime and release changes

- added `tb doctor` for profile detection, backend checks, and runtime diagnostics
- added `tb features` for a concise view of build/runtime capabilities
- added `tb version` for release metadata
- added `tb update` for GitHub-release-based upgrades on supported archive formats
- added `install.sh` for curl-based install on Linux and macOS
- release packaging now ships archives that include the binary plus license files and README

### Direct-send reporting changes

- direct OAuth send support is now reported explicitly by the binary instead of being implied
- unsupported builds degrade honestly instead of pretending the feature should work everywhere
- Linux release builds are intended to be the strongest path for direct provider-aware send
- portable builds remain useful for search/read workflows even when direct send is unavailable

### Documentation changes

- rewrote `README.md` as the actual operator manual, product pitch, and distribution guide
- moved fast install/update to the top of the README
- added explicit guidance for coding-agent workflows, including Codex and Claude Code usage patterns
- split repository licensing so code remains Apache-2.0 and Markdown docs are CC BY-SA 4.0

### Breaking expectations worth noting

- `TB_PG_DSN` is no longer required for normal use
- the default cache location is now the SQLite state directory, not PostgreSQL
- `tb update` is designed around release archives; Windows remains a manual-download path for now

## [0.2.0] - 2026-03-25

This release turned `tb` from a strong local-mail search tool into a credible headless mail operator for the accounts people already keep in Betterbird or Thunderbird.

### Added

- direct provider-aware headless send for:
  - Google / Gmail
  - Microsoft / Outlook / Hotmail / Office 365
  - Yahoo
- native NSS-backed secret decryption so `tb` can reuse the OAuth refresh tokens already stored in the Thunderbird or Betterbird profile
- direct SMTP `XOAUTH2` send for supported providers
- direct IMAP `XOAUTH2` append to the real server-side Sent folder after send
- provider-aware SMTP transport handling:
  - implicit TLS for Gmail and Yahoo
  - `STARTTLS` for Microsoft SMTP on port `587`
- tests for:
  - scalar `prefs.js` parsing
  - Sent-folder URI parsing
  - provider-specific default port selection

### Verified in practice

These flows were exercised against live configured accounts on this machine:

- Gmail direct headless send completed successfully
- Yahoo direct headless send completed successfully
- Outlook direct headless send completed successfully
- Yahoo self-send was verified on the live IMAP server in both `INBOX` and `Sent`
- Outlook self-send was verified on the live IMAP server in both `INBOX` and `Sent`

### Example

```bash
tb mail compose \
  --profile default \
  --from user@gmail.com \
  --to support@example.org \
  --cc audit@example.org \
  --subject "Support request" \
  --body "Hello" \
  --send --open=false
```
