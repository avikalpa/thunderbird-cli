# tb playbook

Goal: quick, safe CLI access to Thunderbird mailboxes using `tb` (default profile root `~/.thunderbird` or `THUNDERBIRD_HOME`). All data access is read-only; the only write is an optional cache `.tb-index.json` under the profile.

## Build
```sh
cd ~/git/thunderbird-cli
go build -o bin/tb ./...
```

## Fast starts
- List profiles: `tb mail profiles`
- List folders: `tb mail folders --profile myprofile`
- Recent in a folder: `tb mail recent Inbox --profile myprofile --limit 10 --query invoice`

## Searching (parsed, table output)
- Simple: `tb search "invoice" --profile myprofile --limit 20`
- Account scoped: `tb search --profile myprofile --account user@example.com "tax"`
- Date range: `tb search --profile myprofile --since 2024-01-01 --till 2024-12-31 "keyword"`
- Tail bound (scan last N per folder): `tb search --profile myprofile --tail 5000 "keyword"`
- Folder filter: `tb search --profile myprofile --folder ImapMail/mail.example.com/INBOX "keyword"`
- Skip cache: `tb search --no-index "keyword"`
- Plain/LLM output: add `--no-fancy`
- Include Trash/Spam explicitly: add `--include-trash`/`--include-spam`

### Example timelines
- Billing history across folders:  
  `tb search --profile myprofile --limit 0 "bill"`
- Specific provider inbox:  
  `tb search --profile myprofile --folder ImapMail/mail.example.com/INBOX --limit 0 "provider name"`
- Legal keyword window:  
  `tb search --profile myprofile --since 2023-01-01 --till 2024-01-01 --limit 0 "court"`

### Hard search drills (for robustness)
- Account-scoped keyword:  
  `tb search --profile myprofile --account user@example.com --limit 0 "keyword"`
- All accounts keyword:  
  `tb search --profile myprofile --limit 0 "keyword"`
- Date-bounded common word:  
  `tb search --profile myprofile --since 2023-01-01 --till 2024-01-01 "commonword" --limit 0`

## Indexing (optional cache for speed)
- Whole profile with defaults:  
  `tb mail index --profile myprofile`
- Account + folder focus:  
  `tb mail index --profile myprofile --account user@example.com --folder INBOX --tail 8000`
- Disable tail (full scan—may be slow): add `--tail 0`
- Include trash/spam only if needed: add `--include-trash`/`--include-spam`

Notes on indexing:
- Cache file: `<profile>/.tb-index.json`. Delete it to drop the cache; use `--no-index` to bypass it temporarily.
- Index uses file mtime/size to detect staleness. We never modify mbox/config data.

## Raw grep-like search (fast, no MIME parsing)
- Fast text hits: `tb search --profile myprofile --raw "keyword" --limit 50`
- Plain output: add `--no-fancy`

## Safety
- Read-only for mailboxes/config; sending uses Thunderbird itself.
- Only write: `.tb-index.json` cache.
- Folder filters are fuzzy but case-insensitive; use `tb mail folders` to copy exact names.
## Test runner
- `./tests/run.sh` builds, runs unit tests, and (optionally) small integration searches when a Thunderbird profile is available.
