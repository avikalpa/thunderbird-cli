#!/usr/bin/env sh
set -eu

if [ $# -ne 1 ]; then
  echo "usage: $0 <version-or-tag>" >&2
  exit 1
fi

version="$1"
version=${version#v}

awk -v version="$version" '
  BEGIN {
    in_section = 0
    found = 0
  }
  $0 ~ "^## \\[" version "\\]" {
    in_section = 1
    found = 1
  }
  in_section {
    if (NR > 1 && $0 ~ /^## \[/ && $0 !~ "^## \\[" version "\\]") {
      exit
    }
    print
  }
  END {
    if (!found) {
      exit 2
    }
  }
' CHANGELOG.md
