package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	storeBackendSQLite   = "sqlite"
	storeBackendPostgres = "postgres"
)

type messageStore interface {
	Close()
	Upsert(ctx context.Context, msgs []MailSummary) error
	Search(ctx context.Context, q queryOptions) ([]MailSummary, error)
	CountMessages(ctx context.Context, profile string) (int64, error)
	SetMeta(ctx context.Context, key, val string) error
	GetMeta(ctx context.Context, key string) (string, error)
	GetMetaPrefix(ctx context.Context, prefix string) (map[string]string, error)
	PruneMissing(ctx context.Context, profile string, keepIDs []string) error
	Info() StoreInfo
}

type StoreInfo struct {
	Backend  string
	Location string
}

func openStore() (messageStore, error) {
	switch selectedStoreBackend() {
	case storeBackendPostgres:
		return openPG()
	case storeBackendSQLite:
		return openSQLiteStore(defaultSQLitePath())
	default:
		return nil, fmt.Errorf("unsupported store backend %q", selectedStoreBackend())
	}
}

func selectedStoreBackend() string {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("TB_STORE")))
	if backend == "" {
		return storeBackendSQLite
	}
	return backend
}

func defaultSQLitePath() string {
	if path := strings.TrimSpace(os.Getenv("TB_SQLITE_PATH")); path != "" {
		return path
	}
	root := detectThunderbirdRoot()
	return filepath.Join(stateDir("thunderbird-cli"), rootHash(root), "index-v1.db")
}

func stateDir(app string) string {
	if xdg := strings.TrimSpace(os.Getenv("XDG_STATE_HOME")); xdg != "" {
		return filepath.Join(xdg, app)
	}
	home := strings.TrimSpace(os.Getenv("HOME"))
	if home == "" {
		home = "."
	}
	return filepath.Join(home, ".local", "state", app)
}

func rootHash(root string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	return hex.EncodeToString(sum[:8])
}

func mustMkdirParent(path string) error {
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0o755)
}

func formatStoreTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
