package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type sqliteStore struct {
	db   *sql.DB
	path string
}

func openSQLiteStore(path string) (*sqliteStore, error) {
	if err := mustMkdirParent(path); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &sqliteStore{db: db, path: path}
	if err := store.ensureSchema(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *sqliteStore) Info() StoreInfo {
	return StoreInfo{
		Backend:  storeBackendSQLite,
		Location: s.path,
	}
}

func (s *sqliteStore) ensureSchema(ctx context.Context) error {
	stmts := []string{
		`PRAGMA journal_mode=WAL;`,
		`PRAGMA synchronous=NORMAL;`,
		`PRAGMA temp_store=MEMORY;`,
		`CREATE TABLE IF NOT EXISTS tb_messages (
			rowid INTEGER PRIMARY KEY,
			profile TEXT NOT NULL,
			message_id TEXT NOT NULL,
			folder TEXT NOT NULL,
			subject TEXT,
			sender TEXT,
			snippet TEXT,
			search_text TEXT,
			when_ts INTEGER,
			date_str TEXT,
			account TEXT,
			UNIQUE(profile, message_id)
		);`,
		`CREATE TABLE IF NOT EXISTS tb_meta (
			key TEXT PRIMARY KEY,
			val TEXT
		);`,
		`CREATE INDEX IF NOT EXISTS tb_messages_when_idx ON tb_messages (profile, when_ts DESC);`,
		`CREATE INDEX IF NOT EXISTS tb_messages_folder_idx ON tb_messages (profile, folder);`,
		`CREATE INDEX IF NOT EXISTS tb_messages_account_idx ON tb_messages (profile, account);`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS tb_messages_fts USING fts5(
			subject,
			sender,
			snippet,
			search_text,
			content='tb_messages',
			content_rowid='rowid',
			tokenize='unicode61 remove_diacritics 2'
		);`,
		`CREATE TRIGGER IF NOT EXISTS tb_messages_ai AFTER INSERT ON tb_messages BEGIN
			INSERT INTO tb_messages_fts(rowid, subject, sender, snippet, search_text)
			VALUES (new.rowid, new.subject, new.sender, new.snippet, new.search_text);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS tb_messages_ad AFTER DELETE ON tb_messages BEGIN
			INSERT INTO tb_messages_fts(tb_messages_fts, rowid, subject, sender, snippet, search_text)
			VALUES ('delete', old.rowid, old.subject, old.sender, old.snippet, old.search_text);
		END;`,
		`CREATE TRIGGER IF NOT EXISTS tb_messages_au AFTER UPDATE ON tb_messages BEGIN
			INSERT INTO tb_messages_fts(tb_messages_fts, rowid, subject, sender, snippet, search_text)
			VALUES ('delete', old.rowid, old.subject, old.sender, old.snippet, old.search_text);
			INSERT INTO tb_messages_fts(rowid, subject, sender, snippet, search_text)
			VALUES (new.rowid, new.subject, new.sender, new.snippet, new.search_text);
		END;`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func (s *sqliteStore) Close() {
	_ = s.db.Close()
}

func (s *sqliteStore) Upsert(ctx context.Context, msgs []MailSummary) error {
	if len(msgs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `
INSERT INTO tb_messages (profile, message_id, folder, subject, sender, snippet, search_text, when_ts, date_str, account)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(profile, message_id) DO UPDATE SET
	folder=excluded.folder,
	subject=excluded.subject,
	sender=excluded.sender,
	snippet=excluded.snippet,
	search_text=excluded.search_text,
	when_ts=excluded.when_ts,
	date_str=excluded.date_str,
	account=excluded.account
`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	defer stmt.Close()
	for _, m := range msgs {
		whenUnix := int64(0)
		if !m.When.IsZero() {
			whenUnix = m.When.UTC().Unix()
		}
		if _, err := stmt.ExecContext(ctx,
			m.Profile,
			forceUTF8(m.MessageID),
			forceUTF8(m.Folder),
			forceUTF8(m.Subject),
			forceUTF8(m.From),
			forceUTF8(m.Snippet),
			forceUTF8(m.Search),
			whenUnix,
			forceUTF8(m.Date),
			forceUTF8(m.Account),
		); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("sqlite upsert msg=%s folder=%s: %w", m.MessageID, m.Folder, err)
		}
	}
	return tx.Commit()
}

func (s *sqliteStore) Search(ctx context.Context, q queryOptions) ([]MailSummary, error) {
	var (
		joins []string
		where []string
		args  []interface{}
	)
	add := func(v interface{}) string {
		args = append(args, v)
		return "?"
	}

	if q.ftsExpr != "" || q.query != "" {
		match := q.ftsExpr
		if match == "" {
			match = ftsQuery(q.query)
		}
		if match != "" {
			joins = append(joins, "JOIN tb_messages_fts fts ON fts.rowid = m.rowid")
			where = append(where, fmt.Sprintf("tb_messages_fts MATCH %s", add(match)))
		}
	}
	if q.profile != "" {
		where = append(where, fmt.Sprintf("m.profile = %s", add(q.profile)))
	}
	if q.account != "" {
		where = append(where, fmt.Sprintf("m.account = %s", add(strings.ToLower(q.account))))
	}
	if q.folderLike != "" {
		where = append(where, fmt.Sprintf("LOWER(m.folder) LIKE '%%' || LOWER(%s) || '%%'", add(q.folderLike)))
	}
	if !q.since.IsZero() {
		where = append(where, fmt.Sprintf("m.when_ts >= %s", add(q.since.UTC().Unix())))
	}
	if !q.till.IsZero() {
		where = append(where, fmt.Sprintf("m.when_ts < %s", add(q.till.UTC().Unix())))
	}
	query := `
SELECT m.profile, m.message_id, m.folder, m.subject, m.sender, m.snippet, m.search_text, m.when_ts, m.date_str, m.account
FROM tb_messages m
`
	if len(joins) > 0 {
		query += strings.Join(joins, "\n") + "\n"
	}
	if len(where) > 0 {
		query += "WHERE " + strings.Join(where, " AND ") + "\n"
	}
	order := "ORDER BY m.when_ts DESC, m.date_str DESC"
	if q.ftsExpr != "" || q.query != "" {
		order = "ORDER BY bm25(tb_messages_fts), m.when_ts DESC, m.date_str DESC"
	}
	query += order + "\n"
	if q.limit > 0 {
		query += fmt.Sprintf("LIMIT %d\n", q.limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MailSummary
	for rows.Next() {
		var (
			m        MailSummary
			whenUnix int64
		)
		if err := rows.Scan(&m.Profile, &m.MessageID, &m.Folder, &m.Subject, &m.From, &m.Snippet, &m.Search, &whenUnix, &m.Date, &m.Account); err != nil {
			return nil, err
		}
		if whenUnix > 0 {
			m.When = time.Unix(whenUnix, 0).UTC()
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func ftsQuery(q string) string {
	return ftsExpression(q, "AND", false)
}

// ftsExpression builds an FTS5 MATCH expression from a plain query.
//
// join is "AND" (every token must appear) or "OR" (any token, ranked by bm25).
// prefix appends the FTS5 prefix operator so "invoic" also matches "invoices" —
// exact-token-only matching is a common reason a search that should obviously
// hit returns nothing.
func ftsExpression(q, join string, prefix bool) string {
	tokens := strings.Fields(strings.TrimSpace(q))
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		quoted := `"` + strings.ReplaceAll(t, `"`, `""`) + `"`
		if prefix {
			quoted += "*"
		}
		out = append(out, quoted)
	}
	return strings.Join(out, " "+join+" ")
}

func (s *sqliteStore) CountMessages(ctx context.Context, profile string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM tb_messages WHERE profile = ?`, profile).Scan(&n)
	return n, err
}

func (s *sqliteStore) SetMeta(ctx context.Context, key, val string) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO tb_meta (key, val) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET val=excluded.val
`, key, val)
	return err
}

func (s *sqliteStore) GetMeta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT val FROM tb_meta WHERE key = ?`, key).Scan(&v)
	return v, err
}

func (s *sqliteStore) GetMetaPrefix(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, val FROM tb_meta WHERE key LIKE ?`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, val string
		if err := rows.Scan(&key, &val); err != nil {
			return nil, err
		}
		out[key] = val
	}
	return out, rows.Err()
}

func (s *sqliteStore) PruneMissing(ctx context.Context, profile string, keepIDs []string) error {
	if len(keepIDs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `CREATE TEMP TABLE IF NOT EXISTS tb_keep_ids (message_id TEXT PRIMARY KEY)`); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM tb_keep_ids`); err != nil {
		_ = tx.Rollback()
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT OR IGNORE INTO tb_keep_ids(message_id) VALUES (?)`)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	for _, id := range keepIDs {
		if _, err := stmt.ExecContext(ctx, id); err != nil {
			stmt.Close()
			_ = tx.Rollback()
			return err
		}
	}
	stmt.Close()
	if _, err := tx.ExecContext(ctx, `
DELETE FROM tb_messages
WHERE profile = ?
  AND NOT EXISTS (
    SELECT 1 FROM tb_keep_ids k WHERE k.message_id = tb_messages.message_id
  )
`, profile); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func sqliteRuntimeAvailable() bool {
	path := defaultSQLitePath()
	if err := mustMkdirParent(path); err != nil {
		return false
	}
	f, err := os.CreateTemp(filepath.Dir(path), "tb-sqlite-check-*.db")
	if err != nil {
		return false
	}
	_ = f.Close()
	_ = os.Remove(f.Name())
	return true
}
