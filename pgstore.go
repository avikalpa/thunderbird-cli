package main

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type pgStore struct {
	pool *pgxpool.Pool
}

func openPG() (*pgStore, error) {
	dsn := strings.TrimSpace(os.Getenv("TB_PG_DSN"))
	if dsn == "" {
		return nil, fmt.Errorf("TB_PG_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	store := &pgStore{pool: pool}
	if err := store.ensureSchema(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return store, nil
}

func (s *pgStore) ensureSchema(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
CREATE TABLE IF NOT EXISTS tb_messages (
  message_id text NOT NULL,
  folder text NOT NULL,
  subject text,
  sender text,
  snippet text,
  search_text text,
  when_ts timestamptz,
  date_str text,
  account text
);
CREATE TABLE IF NOT EXISTS tb_meta (
  key text PRIMARY KEY,
  val text
);
ALTER TABLE tb_messages ADD COLUMN IF NOT EXISTS profile text NOT NULL DEFAULT '';
DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint c
    JOIN pg_class t ON c.conrelid = t.oid
    WHERE t.relname = 'tb_messages' AND c.conname = 'tb_messages_pkey'
      AND pg_get_constraintdef(c.oid) LIKE '%(profile, message_id)%'
  ) THEN
    BEGIN
      ALTER TABLE tb_messages DROP CONSTRAINT IF EXISTS tb_messages_pkey;
    EXCEPTION WHEN undefined_object THEN NULL;
    END;
    ALTER TABLE tb_messages ADD CONSTRAINT tb_messages_pkey PRIMARY KEY (profile, message_id);
  END IF;
END $$;
CREATE INDEX IF NOT EXISTS tb_messages_when_idx ON tb_messages (profile, when_ts DESC NULLS LAST);
CREATE INDEX IF NOT EXISTS tb_messages_search_idx ON tb_messages USING GIN (to_tsvector('simple', coalesce(search_text,'')));
CREATE INDEX IF NOT EXISTS tb_messages_folder_idx ON tb_messages (profile, folder);
CREATE INDEX IF NOT EXISTS tb_messages_account_idx ON tb_messages (profile, account);
`)
	return err
}

func (s *pgStore) Close() {
	s.pool.Close()
}

func (s *pgStore) Upsert(ctx context.Context, msgs []MailSummary) error {
	if len(msgs) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	stmt := `
INSERT INTO tb_messages (profile, message_id, folder, subject, sender, snippet, search_text, when_ts, date_str, account)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (profile, message_id) DO UPDATE
  SET folder=EXCLUDED.folder,
      subject=EXCLUDED.subject,
      sender=EXCLUDED.sender,
      snippet=EXCLUDED.snippet,
      search_text=EXCLUDED.search_text,
      when_ts=EXCLUDED.when_ts,
      date_str=EXCLUDED.date_str,
      account=EXCLUDED.account;
`
	for _, m := range msgs {
		when := m.When
		if when.IsZero() && m.Date != "" {
			if t, err := time.Parse(time.RFC3339, m.Date); err == nil {
				when = t
			}
		}
		subject := forceUTF8(m.Subject)
		sender := forceUTF8(m.From)
		snippet := forceUTF8(m.Snippet)
		search := forceUTF8(m.Search)
		folder := forceUTF8(m.Folder)
		msgID := forceUTF8(m.MessageID)
		dateStr := forceUTF8(m.Date)
		account := forceUTF8(m.Account)
		if _, err := tx.Exec(ctx, stmt, m.Profile, msgID, folder, subject, sender, snippet, search, when, dateStr, account); err != nil {
			log.Printf("upsert failed id=%s folder=%s err=%v", msgID, folder, err)
			return fmt.Errorf("upsert msg=%s folder=%s: %w", msgID, folder, err)
		}
	}
	return tx.Commit(ctx)
}

func forceUTF8(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	b = bytes.ReplaceAll(b, []byte{0x00}, []byte{})
	return string(bytes.ToValidUTF8(b, nil))
}

func (s *pgStore) Search(ctx context.Context, q queryOptions) ([]MailSummary, error) {
	var where []string
	var args []interface{}
	arg := func(v interface{}) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}
	if q.profile != "" {
		where = append(where, fmt.Sprintf("profile = %s", arg(q.profile)))
	}
	if q.query != "" {
		tokens := strings.Fields(strings.ToLower(q.query))
		for _, t := range tokens {
			where = append(where, fmt.Sprintf("search_text ILIKE '%%' || %s || '%%'", arg(t)))
		}
	}
	if q.account != "" {
		where = append(where, fmt.Sprintf("account = %s", arg(strings.ToLower(q.account))))
	}
	if q.folderLike != "" {
		where = append(where, fmt.Sprintf("folder ILIKE '%%' || %s || '%%'", arg(q.folderLike)))
	}
	if !q.since.IsZero() {
		where = append(where, fmt.Sprintf("when_ts >= %s", arg(q.since)))
	}
	if !q.till.IsZero() {
		where = append(where, fmt.Sprintf("when_ts < %s", arg(q.till)))
	}
	clause := "1=1"
	if len(where) > 0 {
		clause = strings.Join(where, " AND ")
	}
	limitClause := ""
	if q.limit > 0 {
		limitClause = fmt.Sprintf("LIMIT %d", q.limit)
	}
	rows, err := s.pool.Query(ctx, fmt.Sprintf(`
SELECT profile, message_id, folder, subject, sender, snippet, search_text, when_ts, date_str, account
FROM tb_messages
WHERE %s
ORDER BY when_ts DESC NULLS LAST, date_str DESC
%s
`, clause, limitClause), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MailSummary
	for rows.Next() {
		var m MailSummary
		var when time.Time
		if err := rows.Scan(&m.Profile, &m.MessageID, &m.Folder, &m.Subject, &m.From, &m.Snippet, &m.Search, &when, &m.Date, &m.Account); err != nil {
			return nil, err
		}
		if !when.IsZero() {
			m.When = when
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

type queryOptions struct {
	query      string
	account    string
	folderLike string
	since      time.Time
	till       time.Time
	limit      int
	profile    string
}

func (s *pgStore) CountMessages(ctx context.Context, profile string) (int64, error) {
	var n int64
	err := s.pool.QueryRow(ctx, `SELECT COUNT(*) FROM tb_messages WHERE profile = $1`, profile).Scan(&n)
	return n, err
}

func (s *pgStore) SetMeta(ctx context.Context, key, val string) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO tb_meta (key, val) VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET val = EXCLUDED.val
`, key, val)
	return err
}

func (s *pgStore) GetMeta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.pool.QueryRow(ctx, `SELECT val FROM tb_meta WHERE key = $1`, key).Scan(&v)
	if err != nil {
		return "", err
	}
	return v, nil
}

func (s *pgStore) GetMetaPrefix(ctx context.Context, prefix string) (map[string]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT key, val FROM tb_meta WHERE key LIKE $1`, prefix+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *pgStore) PruneMissing(ctx context.Context, profile string, keepIDs []string) error {
	if len(keepIDs) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
DELETE FROM tb_messages
WHERE profile = $1 AND message_id <> ALL($2)
`, profile, keepIDs)
	return err
}
