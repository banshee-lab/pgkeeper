package config

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type loader struct {
	main      string
	merged    raw
	src       map[string]string // section -> defining file
	backupSrc map[string]string // backup name -> defining file
}

// Load reads the main file, merges includes, expands env vars, applies
// defaults, validates, and returns the resolved Config. All validation
// problems are returned together (errors.Join).
func Load(path string) (*Config, error) {
	path = filepath.Clean(path)
	mainRaw, err := readRaw(path)
	if err != nil {
		return nil, err
	}

	l := &loader{
		main:      path,
		merged:    raw{Backup: map[string]rawBackup{}},
		src:       map[string]string{},
		backupSrc: map[string]string{},
	}
	errs := l.add(path, mainRaw)

	var patterns []string
	if mainRaw.Global != nil {
		patterns = mainRaw.Global.Include
	}
	dir := filepath.Dir(path)
	seen := map[string]bool{path: true}
	for _, pat := range patterns {
		if !filepath.IsAbs(pat) {
			pat = filepath.Join(dir, pat)
		}
		matches, err := filepath.Glob(pat)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: global.include: invalid glob %q: %w", path, pat, err))
			continue
		}
		slices.Sort(matches)
		for _, m := range matches {
			if seen[m] {
				continue
			}
			seen[m] = true
			if fi, err := os.Stat(m); err == nil && fi.IsDir() {
				continue
			}
			r, err := readRaw(m)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if r.Global != nil {
				r.Global.Include = nil // no recursion
			}
			errs = append(errs, l.add(m, r)...)
		}
	}

	cfg, verrs := l.resolve()
	errs = append(errs, verrs...)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return cfg, nil
}

// readRaw: decode to map, expand env on every string, re-marshal, then decode
// strictly into the typed structs (unknown keys fail).
func readRaw(file string) (*raw, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	generic := map[string]any{}
	if err := toml.Unmarshal(data, &generic); err != nil {
		return nil, fmt.Errorf("config: %s: %w", file, err)
	}
	var errs []error
	expandValue(generic, file, "", &errs)
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	out, err := toml.Marshal(generic)
	if err != nil {
		return nil, fmt.Errorf("config: %s: %w", file, err)
	}

	var r raw
	dec := toml.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		var sme *toml.StrictMissingError
		if errors.As(err, &sme) {
			var keys []error
			for _, de := range sme.Errors {
				keys = append(keys, fmt.Errorf("config: %s: unknown key %q", file, strings.Join(de.Key(), ".")))
			}
			return nil, errors.Join(keys...)
		}
		return nil, fmt.Errorf("config: %s: %w", file, err)
	}
	return &r, nil
}

func claim[T any](l *loader, errs *[]error, section, file string, dst **T, src *T) {
	if src == nil {
		return
	}
	if prev, ok := l.src[section]; ok {
		*errs = append(*errs, fmt.Errorf("%s: section [%s] is already defined in %s", file, section, prev))
		return
	}
	l.src[section] = file
	*dst = src
}

// add merges one file into the loader.
func (l *loader) add(file string, r *raw) []error {
	var errs []error
	claim(l, &errs, "global", file, &l.merged.Global, r.Global)
	claim(l, &errs, "telegram", file, &l.merged.Telegram, r.Telegram)
	claim(l, &errs, "auth", file, &l.merged.Auth, r.Auth)
	claim(l, &errs, "restic", file, &l.merged.Restic, r.Restic)

	for _, name := range slices.Sorted(maps.Keys(r.Backup)) {
		if prev, dup := l.backupSrc[name]; dup {
			errs = append(errs, fmt.Errorf("backup %q is defined in both %s and %s", name, prev, file))
			continue
		}
		l.backupSrc[name] = file
		l.merged.Backup[name] = r.Backup[name]
	}
	return errs
}

func (l *loader) srcOr(section string) string {
	if f, ok := l.src[section]; ok {
		return f
	}
	return l.main
}