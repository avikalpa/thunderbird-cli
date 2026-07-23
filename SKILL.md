---
name: thunderbird-cli
description: Read, search, and reply to mail in an existing Thunderbird or Betterbird profile from the command line. Use when a task needs local mail evidence — finding a message, reading a thread, extracting an attachment, or sending/replying through an already-configured identity.
---

# thunderbird-cli (`tb`)

This file exists so a skill loader has an entry point. **The instructions live in
[AGENTS.md](AGENTS.md)** — one file, so the two cannot drift apart.

## Thirty-second version

```bash
tb doctor                       # what this machine can actually do
tb q "what you are looking for" # one high-recall search; JSON when piped
```

`tb q` searches every account and folder, refreshes a stale cache, ranks by
relevance and widens the match when nothing hits. Every result carries a `read`
field holding the exact command to retrieve that message — use it verbatim.

Read [AGENTS.md](AGENTS.md) before anything beyond that, especially before
sending: `tb reply` is a dry run unless `--send` is passed, and that default is
deliberate.

## Prerequisites

`tb doctor` reports all of these live; trust it over any document.

- **An existing, populated Thunderbird/Betterbird profile.** `tb` reads mail; it
  does not create accounts. Creating or editing a mailbox is a mail-server
  operation, not a `tb` one.
- **Linux (x86-64 or arm64) for full capability.** macOS and Windows builds ship
  and can read, search and cache, but direct send, credential storage,
  `authcheck` and `sentcheck` are compiled only on Linux with cgo and NSS
  (`libnss3`, `libnspr4`). `tb update` is unsupported on Windows.
- **A Thunderbird-family client, for `--sync` only.** Native `betterbird` or
  `thunderbird` on `PATH`, or the Flatpak. Betterbird is recommended. Reading and
  searching an already-synced profile need no client at all.
- **For `--sync` on a headless host**, one of: a GUI session, an already-running
  client to join, or `Xvfb`.
