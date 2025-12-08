package main

import (
	"encoding/json"
	"os"
	"time"
)

type IndexFile struct {
	Folders map[string]FolderIndex `json:"folders"`
	SavedAt time.Time              `json:"saved_at"`
}

type FolderIndex struct {
	ModTime  int64         `json:"mod_time"`
	Size     int64         `json:"size"`
	Messages []MailSummary `json:"messages"`
	SavedAt  time.Time     `json:"saved_at"`
	Complete bool          `json:"complete"` // true when the index was built by scanning the full folder (match-all)
}

func loadIndex(path string) (*IndexFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var idx IndexFile
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, err
	}
	if idx.Folders == nil {
		idx.Folders = map[string]FolderIndex{}
	}
	return &idx, nil
}

func saveIndex(path string, idx *IndexFile) error {
	idx.SavedAt = time.Now().UTC()
	b, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
