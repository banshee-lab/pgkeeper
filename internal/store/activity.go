package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"uuid"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

type ActivityStore struct{ db *sql.DB }

func NewActivityStore(db *sql.DB) *ActivityStore { return &ActivityStore{db: db} }

var _ domain.ActivityRepository = (*ActivityStore)(nil)

const actCols = `id, backup_name, database_name, triggered_by, status, started_at, finished_at, error, log_path`

type scanner interface{ Scan(dest ...any) error }

func scanActivity(s scanner) (domain.Activity, error) {
	var (
		a                       domain.Activity
		id, trig, status, start string
		fin, errMsg             sql.NullString
	)
	if err := s.Scan(&id, &a.BackupName, &a.Database, &trig, &status, &start, &fin, &errMsg, &a.LogPath); err != nil {
		return a, err
	}
	var err error
	if a.ID, err = uuid.Parse(id); err != nil {
		return a, err
	}
	a.TriggeredBy, a.Status = domain.Trigger(trig), domain.Status(status)
	if a.StartedAt, err = parseTime(start); err != nil {
		return a, err
	}
	if fin.Valid {
		t, err := parseTime(fin.String)
		if err != nil {
			return a, err
		}
		a.FinishedAt = &t
	}
	a.Error = errMsg.String
	return a, nil
}

func (s *ActivityStore) Create(ctx context.Context, a *domain.Activity) error {
	var fin, errMsg sql.NullString
	if a.FinishedAt != nil {
		fin = sql.NullString{String: fmtTime(*a.FinishedAt), Valid: true}
	}
	if a.Error != "" {
		errMsg = sql.NullString{String: a.Error, Valid: true}
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO activities (`+actCols+`) VALUES (?,?,?,?,?,?,?,?,?)`,
		a.ID.String(), a.BackupName, a.Database, string(a.TriggeredBy), string(a.Status),
		fmtTime(a.StartedAt), fin, errMsg, a.LogPath)
	return err
}

func (s *ActivityStore) Finish(ctx context.Context, id uuid.UUID, st domain.Status, at time.Time, msg string) error {
	var errMsg sql.NullString
	if msg != "" {
		errMsg = sql.NullString{String: msg, Valid: true}
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE activities SET status=?, finished_at=?, error=? WHERE id=?`,
		string(st), fmtTime(at), errMsg, id.String())
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *ActivityStore) Get(ctx context.Context, id uuid.UUID) (*domain.Activity, error) {
	a, err := scanActivity(s.db.QueryRowContext(ctx,
		`SELECT `+actCols+` FROM activities WHERE id=?`, id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *ActivityStore) query(ctx context.Context, q string, args ...any) ([]domain.Activity, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []domain.Activity{}
	for rows.Next() {
		a, err := scanActivity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *ActivityStore) ListByBackup(ctx context.Context, name string, limit int) ([]domain.Activity, error) {
	return s.query(ctx, `SELECT `+actCols+` FROM activities WHERE backup_name=? ORDER BY started_at DESC LIMIT ?`,
		name, clampLimit(limit))
}

func (s *ActivityStore) ListRecent(ctx context.Context, limit int) ([]domain.Activity, error) {
	return s.query(ctx, `SELECT `+actCols+` FROM activities ORDER BY started_at DESC LIMIT ?`, clampLimit(limit))
}

func (s *ActivityStore) LastByBackup(ctx context.Context, names []string) (map[string]domain.Activity, error) {
	res := map[string]domain.Activity{}
	if len(names) == 0 {
		return res, nil
	}
	args := make([]any, len(names))
	for i, n := range names {
		args[i] = n
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	list, err := s.query(ctx, `SELECT `+actCols+` FROM (
		SELECT *, ROW_NUMBER() OVER (PARTITION BY backup_name ORDER BY started_at DESC) AS rn
		FROM activities WHERE backup_name IN (`+ph+`)) WHERE rn=1`, args...)
	if err != nil {
		return nil, err
	}
	for _, a := range list {
		res[a.BackupName] = a
	}
	return res, nil
}

func (s *ActivityStore) LastSuccessByBackup(ctx context.Context, names []string) (map[string]time.Time, error) {
	res := map[string]time.Time{}
	if len(names) == 0 {
		return res, nil
	}
	args := make([]any, len(names))
	for i, n := range names {
		args[i] = n
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(names)), ",")
	rows, err := s.db.QueryContext(ctx,
		`SELECT backup_name, MAX(finished_at) FROM activities
		 WHERE backup_name IN (`+ph+`) AND status='success' AND finished_at IS NOT NULL
		 GROUP BY backup_name`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, ts string
		if err := rows.Scan(&name, &ts); err != nil {
			return nil, err
		}
		t, err := parseTime(ts)
		if err != nil {
			return nil, err
		}
		res[name] = t
	}
	return res, rows.Err()
}

// encodeCursor produces an opaque string from (started_at, id).
func encodeCursor(t time.Time, id uuid.UUID) string {
	raw := fmtTime(t) + "|" + id.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor parses a cursor returned by encodeCursor.
func decodeCursor(s string) (time.Time, uuid.UUID, error) {
	b, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, uuid.UUID{}, fmt.Errorf("invalid cursor")
	}
	parts := strings.SplitN(string(b), "|", 2)
	if len(parts) != 2 {
		return time.Time{}, uuid.UUID{}, fmt.Errorf("invalid cursor")
	}
	t, err := parseTime(parts[0])
	if err != nil {
		return time.Time{}, uuid.UUID{}, fmt.Errorf("invalid cursor")
	}
	id, err := uuid.Parse(parts[1])
	if err != nil {
		return time.Time{}, uuid.UUID{}, fmt.Errorf("invalid cursor")
	}
	return t, id, nil
}

func (s *ActivityStore) ListFiltered(ctx context.Context, f domain.ActivityFilter) ([]domain.Activity, string, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 10
	}
	if limit > 100 {
		limit = 100
	}

	var (
		conds []string
		args  []any
	)

	if f.BackupName != "" {
		conds = append(conds, "backup_name=?")
		args = append(args, f.BackupName)
	}
	if f.Status != "" {
		conds = append(conds, "status=?")
		args = append(args, string(f.Status))
	}
	if !f.Since.IsZero() {
		conds = append(conds, "started_at>=?")
		args = append(args, fmtTime(f.Since))
	}
	if f.Cursor != "" {
		cursorTS, cursorID, err := decodeCursor(f.Cursor)
		if err != nil {
			return nil, "", err
		}
		// Descending: rows before the cursor have started_at < cursorTS, or
		// same timestamp with id < cursorID.
		conds = append(conds, "(started_at < ? OR (started_at = ? AND id < ?))")
		args = append(args, fmtTime(cursorTS), fmtTime(cursorTS), cursorID.String())
	}

	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	// Fetch limit+1 to detect whether a next page exists.
	args = append(args, limit+1)
	items, err := s.query(ctx,
		`SELECT `+actCols+` FROM activities`+where+` ORDER BY started_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", err
	}

	var next string
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		next = encodeCursor(last.StartedAt, last.ID)
	}
	return items, next, nil
}

func (s *ActivityStore) MarkRunningAsInterrupted(ctx context.Context, now time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE activities SET status='error', finished_at=?, error='interrupted by restart' WHERE status='running'`,
		fmtTime(now))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}