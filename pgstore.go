package main

import (
	"context"
	"fmt"
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
  message_id text PRIMARY KEY,
  folder text NOT NULL,
  subject text,
  sender text,
  snippet text,
  search_text text,
  when_ts timestamptz,
  date_str text,
  account text
);
CREATE INDEX IF NOT EXISTS tb_messages_when_idx ON tb_messages (when_ts DESC NULLS LAST);
CREATE INDEX IF NOT EXISTS tb_messages_search_idx ON tb_messages USING GIN (to_tsvector('simple', coalesce(search_text,'')));
CREATE INDEX IF NOT EXISTS tb_messages_folder_idx ON tb_messages (folder);
CREATE INDEX IF NOT EXISTS tb_messages_account_idx ON tb_messages (account);
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
INSERT INTO tb_messages (message_id, folder, subject, sender, snippet, search_text, when_ts, date_str, account)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
ON CONFLICT (message_id) DO UPDATE
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
		if _, err := tx.Exec(ctx, stmt, m.MessageID, m.Folder, m.Subject, m.From, m.Snippet, m.Search, when, m.Date, m.Account); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *pgStore) Search(ctx context.Context, q queryOptions) ([]MailSummary, error) {
	var where []string
	var args []interface{}
	arg := func(v interface{}) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
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
SELECT message_id, folder, subject, sender, snippet, search_text, when_ts, date_str, account
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
		if err := rows.Scan(&m.MessageID, &m.Folder, &m.Subject, &m.From, &m.Snippet, &m.Search, &when, &m.Date, &m.Account); err != nil {
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
}
