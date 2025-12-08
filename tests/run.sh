#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${BIN:-"$ROOT/bin/tb"}"
THB_HOME="${THUNDERBIRD_HOME:-"$HOME/.thunderbird"}"

echo "== go test =="
go test ./...

echo "== build =="
go build -o "$BIN" ./...

echo "== tb help =="
$BIN help || true

echo "== optional Thunderbird integration =="
if [ -f "$THB_HOME/profiles.ini" ]; then
  set +e
  profiles_out=$("$BIN" mail profiles 2>/dev/null)
  rc=$?
  set -e
  if [ $rc -eq 0 ]; then
    first_profile=$(printf "%s\n" "$profiles_out" | awk 'NR>1 {print $1; exit}')
    profile="${TB_PROFILE:-$first_profile}"
    if [ -n "${profile:-}" ]; then
      echo "Using profile: $profile"
      "$BIN" mail folders --profile "$profile" | head -n 5 || true
      "$BIN" search --profile "$profile" --limit 3 "test" || true
    else
      echo "No profile rows found; skipping integration searches."
    fi
  else
    echo "Profiles command failed; skipping integration searches."
  fi
else
  echo "No Thunderbird profiles.ini found at $THB_HOME; skipping integration searches."
fi

echo "Tests finished."
