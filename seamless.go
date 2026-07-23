package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// boolFlags do NOT consume a following value. Anything else starting with "-"
// is assumed to take the next token as its value.
var boolFlags = map[string]bool{
	"--raw": true, "--no-fancy": true, "--refresh": true, "--full-rescan": true,
	"--fuzzy": true, "--exact": true, "--sync": true, "--prune": true,
	"--full": true, "--thread": true, "--open": true, "--send": true,
	"-h": true, "--help": true,
}

// reorderArgs moves flags ahead of positionals so that
//
//	tb mail search "query" --profile x
//
// behaves identically to
//
//	tb mail search --profile x "query"
//
// pflag stops parsing at the first positional, which previously made a trailing
// --profile silently ignored: the search then ran against the default profile
// and reported "No matches" while the mail sat in another profile.
func reorderArgs(args []string) []string {
	flags := make([]string, 0, len(args))
	positional := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if strings.Contains(a, "=") || boolFlags[a] {
				continue
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

// profileMailBytes reports how much mbox data a profile actually holds, so an
// empty profiles.ini "Default" profile is never selected over a populated one.
func profileMailBytes(p Profile) int64 {
	var total int64
	for _, sub := range []string{"ImapMail", "Mail"} {
		root := filepath.Join(p.AbsolutePath, sub)
		filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info == nil || info.IsDir() {
				return nil
			}
			switch filepath.Ext(path) {
			case ".msf", ".dat", ".json", ".sqlite":
				return nil
			}
			total += info.Size()
			return nil
		})
	}
	return total
}

// autoRefreshWindow is how stale the cache may be before a search refreshes it
// first. Override with TB_AUTO_REFRESH_MINUTES; 0 disables.
func autoRefreshWindow() time.Duration {
	if v := strings.TrimSpace(os.Getenv("TB_AUTO_REFRESH_MINUTES")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Minute
		}
	}
	return 10 * time.Minute
}
