package api

import (
	"regexp"
	"time"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

type ActivityDTO struct {
	ID          string  `json:"id"`
	BackupName  string  `json:"backup_name"`
	Database    string  `json:"database"`
	Kind        string  `json:"kind"`  // backup | test_restore | restore
	Label       string  `json:"label"` // Backup | Test Restore | Container Restore
	TriggeredBy string  `json:"triggered_by"`
	Status      string  `json:"status"`
	StartedAt   string  `json:"started_at"`
	FinishedAt  *string `json:"finished_at"`
	DurationMS  *int64  `json:"duration_ms"`
	Error       *string `json:"error"`
	LogURL      string  `json:"log_url"`
}

type ResticDTO struct {
	Repository  string `json:"repository"`
	Tag         string `json:"tag"`
	KeepDaily   int    `json:"keep_daily,omitempty"`
	KeepWeekly  int    `json:"keep_weekly,omitempty"`
	KeepMonthly int    `json:"keep_monthly,omitempty"`
	KeepYearly  int    `json:"keep_yearly,omitempty"`
}

type BackupDTO struct {
	Name          string       `json:"name"`
	Cron          string       `json:"cron"`
	Timezone      string       `json:"timezone"`
	Container     string       `json:"container"`
	Database      string       `json:"database"`
	Username      string       `json:"username"`
	Host          string       `json:"host"`
	Port          int          `json:"port"`
	Roles         bool         `json:"roles"`
	RestoreCron   string       `json:"restore_cron"`  // scheduled test restores; empty = disabled
	RestoreImage  string       `json:"restore_image"` // image of the ephemeral test-restore container
	Restic        ResticDTO    `json:"restic"`
	Running       bool         `json:"running"`
	NextRun       *string      `json:"next_run"`
	LastSuccessAt *string      `json:"last_success_at"`
	LastActivity  *ActivityDTO `json:"last_activity"`

	// LatestSnapshot is the snapshot created by the latest successful restic
	// backup, or null if none was recorded yet.
	LatestSnapshot *SnapshotDTO `json:"latest_snapshot"`
}

type SnapshotDTO struct {
	ID           string `json:"id"`
	Timestamp    string `json:"timestamp"`
	FilesAdded   int64  `json:"files_added"`
	FilesChanged int64  `json:"files_changed"`
	BytesAdded   int64  `json:"bytes_added"`
}

func ToSnapshotDTO(s domain.SnapshotInfo) SnapshotDTO {
	return SnapshotDTO{
		ID: s.ID, Timestamp: fmtTime(s.Timestamp),
		FilesAdded: s.FilesAdded, FilesChanged: s.FilesChanged, BytesAdded: s.BytesAdded,
	}
}

type JobDTO struct {
	ActivityID  string `json:"activity_id"`
	BackupName  string `json:"backup_name"`
	Database    string `json:"database"`
	Kind        string `json:"kind"`
	TriggeredBy string `json:"triggered_by"`
	Status      string `json:"status"` // always "running"
	Phase       string `json:"phase"`
	StartedAt   string `json:"started_at"`
}

type JobFinishedDTO struct {
	Job      JobDTO      `json:"job"`
	Activity ActivityDTO `json:"activity"`
}

type LogLineDTO struct {
	Line string `json:"line"`
}

type LogEndDTO struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type UserDTO struct {
	Subject string `json:"subject"`
	Email   string `json:"email"`
	Name    string `json:"name"`
}

type MeDTO struct {
	AuthEnabled   bool     `json:"auth_enabled"`
	Authenticated bool     `json:"authenticated"`
	User          *UserDTO `json:"user"`
}

type ListDTO[T any] struct {
	Items []T `json:"items"`
}

// PagedListDTO is used by the paginated activity list (R-8).
type PagedListDTO[T any] struct {
	Items  []T     `json:"items"`
	Next   *string `json:"next,omitempty"` // opaque cursor; absent when no more pages
}

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func timePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := fmtTime(*t)
	return &s
}

func timePtrVal(t time.Time, ok bool) *string {
	if !ok {
		return nil
	}
	s := fmtTime(t)
	return &s
}

func ToActivityDTO(a domain.Activity) ActivityDTO {
	d := ActivityDTO{
		ID:          a.ID.String(),
		BackupName:  a.BackupName,
		Database:    a.Database,
		Kind:        string(a.EffectiveKind()),
		Label:       a.EffectiveKind().Label(),
		TriggeredBy: string(a.TriggeredBy),
		Status:      string(a.Status),
		StartedAt:   fmtTime(a.StartedAt),
		FinishedAt:  timePtr(a.FinishedAt),
		LogURL:      "/api/v1/activities/" + a.ID.String() + "/log",
	}
	if dur := a.Duration(); dur != nil {
		ms := dur.Milliseconds()
		d.DurationMS = &ms
	}
	if a.Error != "" {
		e := a.Error
		d.Error = &e
	}
	return d
}

func ToActivityDTOs(in []domain.Activity) []ActivityDTO {
	out := make([]ActivityDTO, 0, len(in))
	for _, a := range in {
		out = append(out, ToActivityDTO(a))
	}
	return out
}

// Matches "scheme://user:pass@" and "rest:https://user:pass@" forms.
var credsRe = regexp.MustCompile(`(://)[^/@\s]+@`)

func redactRepo(s string) string { return credsRe.ReplaceAllString(s, "${1}***@") }

// ToBackupDTO: next is the scheduler's next run (nil if unknown), last the
// latest activity (nil if none), lastSuccessAt the last successful run time
// (zero, false if never), tz the effective IANA timezone name.
func ToBackupDTO(b domain.BackupConfig, running bool, next *time.Time, last *domain.Activity, lastSuccessAt time.Time, hasSuccess bool, tz string) BackupDTO {
	d := BackupDTO{
		Name: b.Name, Cron: b.Cron, Timezone: tz,
		Container: b.Container, Database: b.Database,
		Username: b.Username, Host: b.Host, Port: b.Port, Roles: b.Roles,
		RestoreCron: b.RestoreCron, RestoreImage: b.RestoreImage,
		Restic: ResticDTO{
			Repository:  redactRepo(b.Restic.Repository),
			Tag:         b.ResticTag,
			KeepDaily:   b.Restic.KeepDaily,
			KeepWeekly:  b.Restic.KeepWeekly,
			KeepMonthly: b.Restic.KeepMonthly,
			KeepYearly:  b.Restic.KeepYearly,
		},
		Running:       running,
		NextRun:       timePtr(next),
		LastSuccessAt: timePtrVal(lastSuccessAt, hasSuccess),
	}
	if last != nil {
		a := ToActivityDTO(*last)
		d.LastActivity = &a
	}
	return d
}

func ToJobDTO(j domain.Job) JobDTO {
	return JobDTO{
		ActivityID:  j.ActivityID.String(),
		BackupName:  j.BackupName,
		Database:    j.Database,
		Kind:        string(kindOrBackup(j.Kind)),
		TriggeredBy: string(j.TriggeredBy),
		Status:      string(domain.StatusRunning),
		Phase:       string(j.Phase),
		StartedAt:   fmtTime(j.StartedAt),
	}
}

func kindOrBackup(k domain.ActivityKind) domain.ActivityKind {
	if k == "" {
		return domain.KindBackup
	}
	return k
}

func ToJobDTOs(in []domain.Job) []JobDTO {
	out := make([]JobDTO, 0, len(in))
	for _, j := range in {
		out = append(out, ToJobDTO(j))
	}
	return out
}

func ToMeDTO(authEnabled bool, s *domain.Session) MeDTO {
	m := MeDTO{AuthEnabled: authEnabled}
	if s != nil {
		m.Authenticated = true
		m.User = &UserDTO{Subject: s.Subject, Email: s.Email, Name: s.Name}
	}
	return m
}