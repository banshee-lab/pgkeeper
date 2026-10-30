package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Only the ${...} form is recognised; "$$" is not special.
var envRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// expandString expands ${VAR} and ${VAR:-default}. Names of unset variables
// (without default) are returned in missing.
func expandString(s string) (out string, missing []string) {
	out = envRe.ReplaceAllStringFunc(s, func(m string) string {
		sub := envRe.FindStringSubmatch(m)
		v, ok := os.LookupEnv(sub[1])
		if strings.Contains(m, ":-") { // has default: used when unset OR empty
			if !ok || v == "" {
				return sub[2]
			}
			return v
		}
		if !ok {
			missing = append(missing, sub[1])
			return m
		}
		return v
	})
	return out, missing
}

// expandValue walks a decoded TOML tree and expands every string in place.
func expandValue(v any, file, path string, errs *[]error) any {
	switch t := v.(type) {
	case string:
		out, missing := expandString(t)
		for _, name := range missing {
			*errs = append(*errs, fmt.Errorf(
				"config: environment variable %s is not set (in %s at key %s)", name, file, path))
		}
		return out
	case map[string]any:
		for k, val := range t {
			t[k] = expandValue(val, file, joinKey(path, k), errs)
		}
		return t
	case []any:
		for i := range t {
			t[i] = expandValue(t[i], file, fmt.Sprintf("%s[%d]", path, i), errs)
		}
		return t
	}
	return v
}

func joinKey(p, k string) string {
	if p == "" {
		return k
	}
	return p + "." + k
}

var _ = errors.Join // keep import tidy if helpers move