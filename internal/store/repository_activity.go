package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

type RepositoryActivityStore struct{ db *sql.DB }

func NewRepositoryActivityStore(db *sql.DB) *RepositoryActivityStore {
	return &RepositoryActivityStore{db: db}
}

var _ domain.RepositoryActivityStore = (*RepositoryActivityStore)(nil)

func scanRepositoryActivity(row scanner) (domain.RepositoryActivity, error) {
	var a domain.RepositoryActivity
	var kind, trigger, status, started string
	var finished, errText sql.NullString
	if err := row.Scan(&a.ID, &a.RepositoryID, &kind, &trigger, &status, &started, &finished, &errText, &a.LogPath); err != nil {
		return a, err
	}
	a.Kind, a.TriggeredBy, a.Status = domain.MaintenanceKind(kind), domain.Trigger(trigger), domain.Status(status)
	var err error
	if a.StartedAt, err = parseTime(started); err != nil {
		return a, err
	}
	if finished.Valid {
		t, err := parseTime(finished.String)
		if err != nil {
			return a, err
		}
		a.FinishedAt = &t
	}
	a.Error = errText.String
	return a, nil
}

const repoActivityCols = `id, repository_id, kind, triggered_by, status, started_at, finished_at, error, log_path`

func (s *RepositoryActivityStore) CreateRepositoryActivity(ctx context.Context, a *domain.RepositoryActivity) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO repository_activities (`+repoActivityCols+`) VALUES (?,?,?,?,?,?,?,?,?)`,
		a.ID, a.RepositoryID, string(a.Kind), string(a.TriggeredBy), string(a.Status), fmtTime(a.StartedAt), nil, nil, a.LogPath)
	return err
}

func (s *RepositoryActivityStore) FinishRepositoryActivity(ctx context.Context, id string, status domain.Status, at time.Time, message string) error {
	var msg sql.NullString
	if message != "" {
		msg = sql.NullString{String: message, Valid: true}
	}
	res, err := s.db.ExecContext(ctx, `UPDATE repository_activities SET status=?, finished_at=?, error=? WHERE id=?`,
		string(status), fmtTime(at), msg, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *RepositoryActivityStore) GetRepositoryActivity(ctx context.Context, id string) (*domain.RepositoryActivity, error) {
	a, err := scanRepositoryActivity(s.db.QueryRowContext(ctx, `SELECT `+repoActivityCols+` FROM repository_activities WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (s *RepositoryActivityStore) list(ctx context.Context, query string, args ...any) ([]domain.RepositoryActivity, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]domain.RepositoryActivity, 0)
	for rows.Next() {
		a, err := scanRepositoryActivity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *RepositoryActivityStore) ListRepositoryActivities(ctx context.Context, repoID string, limit int) ([]domain.RepositoryActivity, error) {
	return s.list(ctx, `SELECT `+repoActivityCols+` FROM repository_activities WHERE repository_id=? ORDER BY started_at DESC, id DESC LIMIT ?`, repoID, clampLimit(limit))
}

func (s *RepositoryActivityStore) CurrentRepositoryActivities(ctx context.Context, repoID string) ([]domain.RepositoryActivity, error) {
	return s.list(ctx, `SELECT `+repoActivityCols+` FROM repository_activities WHERE repository_id=? AND status='running' ORDER BY started_at DESC`, repoID)
}

func (s *RepositoryActivityStore) MarkRepositoryActivitiesInterrupted(ctx context.Context, now time.Time) (int, error) {
	res, err := s.db.ExecContext(ctx, `UPDATE repository_activities SET status='error', finished_at=?, error='interrupted by restart' WHERE status='running'`, fmtTime(now))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
