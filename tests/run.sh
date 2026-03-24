#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${BIN:-"$ROOT/bin/tb"}"
detect_thunderbird_home() {
  if [ -n "${THUNDERBIRD_HOME:-}" ]; then
    printf '%s\n' "$THUNDERBIRD_HOME"
    return
  fi

  for candidate in \
    "$HOME/.thunderbird" \
    "$HOME/.var/app/eu.betterbird.Betterbird/.thunderbird" \
    "$HOME/.var/app/org.mozilla.Thunderbird/.thunderbird"
  do
    if [ -f "$candidate/profiles.ini" ] || [ -d "$candidate" ]; then
      printf '%s\n' "$candidate"
      return
    fi
  done

  printf '%s\n' "$HOME/.thunderbird"
}

THB_HOME="$(detect_thunderbird_home)"

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
      if [ -n "${TB_PG_DSN:-}" ]; then
        "$BIN" mail fetch --profile "$profile" --max-messages 200 --tail 200 || true
        "$BIN" search --profile "$profile" --limit 3 "test" || true
      else
        echo "TB_PG_DSN not set; skipping Postgres-backed fetch/search."
      fi
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
