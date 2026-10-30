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
	Name         string       `json:"name"`
	Cron         string       `json:"cron"`
	Container    string       `json:"container"`
	Database     string       `json:"database"`
	Username     string       `json:"username"`
	Host         string       `json:"host"`
	Port         int          `json:"port"`
	Roles        bool         `json:"roles"`
	Restic       ResticDTO    `json:"restic"`
	Running      bool         `json:"running"`
	NextRun      *string      `json:"next_run"`
	LastActivity *ActivityDTO `json:"last_activity"`
}

type JobDTO struct {
	ActivityID  string `json:"activity_id"`
	BackupName  string `json:"backup_name"`
	Database    string `json:"database"`
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

func fmtTime(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func timePtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := fmtTime(*t)
	return &s
}

func ToActivityDTO(a domain.Activity) ActivityDTO {
	d := ActivityDTO{
		ID:          a.ID.String(),
		BackupName:  a.BackupName,
		Database:    a.Database,
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
// latest activity (nil if none).
func ToBackupDTO(b domain.BackupConfig, running bool, next *time.Time, last *domain.Activity) BackupDTO {
	d := BackupDTO{
		Name: b.Name, Cron: b.Cron, Container: b.Container, Database: b.Database,
		Username: b.Username, Host: b.Host, Port: b.Port, Roles: b.Roles,
		Restic: ResticDTO{
			Repository:  redactRepo(b.Restic.Repository),
			Tag:         b.ResticTag,
			KeepDaily:   b.Restic.KeepDaily,
			KeepWeekly:  b.Restic.KeepWeekly,
			KeepMonthly: b.Restic.KeepMonthly,
			KeepYearly:  b.Restic.KeepYearly,
		},
		Running: running,
		NextRun: timePtr(next),
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
		TriggeredBy: string(j.TriggeredBy),
		Status:      string(domain.StatusRunning),
		Phase:       string(j.Phase),
		StartedAt:   fmtTime(j.StartedAt),
	}
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