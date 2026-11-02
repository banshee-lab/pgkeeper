package auth

import (
	"net/url"
	"strings"
)

// SafeRedirect returns `to` only if it is a same-origin relative path;
// otherwise it returns fallback (open-redirect protection).
func SafeRedirect(to, fallback string) string {
	if fallback == "" {
		fallback = "/"
	}
	if to == "" || !strings.HasPrefix(to, "/") || strings.HasPrefix(to, "//") || strings.Contains(to, `\`) {
		return fallback
	}
	for _, r := range to {
		if r < 0x20 || r == 0x7f {
			return fallback
		}
	}
	u, err := url.Parse(to)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return fallback
	}
	return to
}