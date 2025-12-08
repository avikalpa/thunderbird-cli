package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (a *App) buildIndex(profileName, folderLike, accountEmail string, tailCount int) error {
	profile, err := a.resolveProfile(profileName)
	if err != nil {
		return err
	}
	accountEmail = strings.ToLower(strings.TrimSpace(accountEmail))

	boxes, err := a.listMailboxes(profile)
	if err != nil {
		return err
	}
	if accountEmail != "" {
		idx, err := a.loadAccountDirIndex(profile)
		if err != nil {
			return err
		}
		dirs := idx[accountEmail]
		if len(dirs) == 0 {
			return fmt.Errorf("account %s not found in prefs.js", accountEmail)
		}
		var scoped []Mailbox
		for _, b := range boxes {
			for _, d := range dirs {
				if strings.HasPrefix(b.Path, d) {
					scoped = append(scoped, b)
					break
				}
			}
		}
		boxes = scoped
	}

	var filtered []Mailbox
	for _, b := range boxes {
		if folderLike == "" {
			filtered = append(filtered, b)
			continue
		}
		needle := strings.ToLower(folderLike)
		if strings.Contains(strings.ToLower(b.Name), needle) || strings.Contains(strings.ToLower(filepath.Base(b.Name)), needle) {
			filtered = append(filtered, b)
		}
	}
	if len(filtered) == 0 {
		return fmt.Errorf("no folders match %q", folderLike)
	}

	cache := &IndexFile{Folders: map[string]FolderIndex{}}
	for _, b := range filtered {
		fmt.Printf("Indexing %s...\n", b.Name)
		more, err := searchMailbox(b, func(string) bool { return true }, 0, time.Time{}, time.Time{}, tailCount, accountEmail, tailCount)
		if err != nil {
			fmt.Printf("skip %s: %v\n", b.Name, err)
			continue
		}
		if fi, err := os.Stat(b.Path); err == nil {
			cache.Folders[b.Path] = FolderIndex{
				ModTime:  fi.ModTime().Unix(),
				Size:     fi.Size(),
				Messages: more,
				SavedAt:  time.Now().UTC(),
				Complete: true,
			}
		}
	}
	return saveIndex(indexPath(profile), cache)
}
