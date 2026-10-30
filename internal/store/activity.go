package store

import (
	"context"
	"database/sql"
	"errors"
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