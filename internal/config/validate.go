package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"github.com/robfig/cron/v3"
)

// CronParser is shared with the scheduler (phase 3).
var CronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

var labelRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

// imageRe accepts docker image references (name, registry, tag, digest).
var imageRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/@-]{0,254}$`)

func checkImage(v string) error {
	if !imageRe.MatchString(v) {
		return errors.New("must be a docker image reference such as postgres:17")
	}
	return nil
}

func keyErr(file, key, format string, args ...any) error {
	return fmt.Errorf("%s: %s: %s", file, key, fmt.Sprintf(format, args...))
}

func checkSafe(v string) error {
	if strings.HasPrefix(v, "-") {
		return errors.New("must not start with '-'")
	}
	if strings.ContainsAny(v, "\x00\n\r") {
		return errors.New("must not contain NUL or newline characters")
	}
	return nil
}

func checkWritableDir(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("cannot create %q: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, ".pgkeeper-write-check-*")
	if err != nil {
		return fmt.Errorf("%q is not writable: %w", dir, err)
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

func absURL(s string) error {
	u, err := url.Parse(s)
	if err != nil {
		return err
	}
	if !u.IsAbs() || u.Host == "" {
		return errors.New("must be an absolute URL")
	}
	return nil
}

func checkKeeps(file, prefix string, r rawRestic) []error {
	var errs []error
	for _, k := range []struct {
		key string
		v   *int
	}{
		{"keep-daily", r.KeepDaily}, {"keep-weekly", r.KeepWeekly},
		{"keep-monthly", r.KeepMonthly}, {"keep-yearly", r.KeepYearly},
	} {
		if k.v != nil && *k.v < 0 {
			errs = append(errs, keyErr(file, prefix+k.key, "must be >= 0 (got %d)", *k.v))
		}
	}
	return errs
}

func firstSet[T any](vals ...*T) T {
	for _, v := range vals {
		if v != nil {
			return *v
		}
	}
	var zero T
	return zero
}

func deref[T any](p *T, def T) T {
	if p != nil {
		return *p
	}
	return def
}