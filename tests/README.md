# Tests

The test runner is a lightweight sanity check that works in two modes:

- Always: `go test ./...`, build `bin/tb`, and verify `tb help` runs.
- Optional integration: if a Thunderbird profile exists (`THUNDERBIRD_HOME` or `~/.thunderbird` with `profiles.ini`), it will list a profile, sample folders, and run a small search. Skips automatically when no profiles are found.

Usage:
```sh
./tests/run.sh
```

Environment:
- `BIN` — path for the built CLI (default `./bin/tb`)
- `THUNDERBIRD_HOME` — Thunderbird root (defaults to `~/.thunderbird`)
- `TB_PROFILE` — profile name to use for the optional integration checks (defaults to the first profile).
