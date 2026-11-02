package auth

import "testing"

func TestSafeRedirect(t *testing.T) {
	for in, want := range map[string]string{
		"/backups":            "/backups",
		"/a/b?x=1#frag":       "/a/b?x=1#frag",
		"":                    "/home",
		"//evil.com":          "/home",
		"https://evil":        "/home",
		`/\evil`:              "/home",
		"evil.com":            "/home",
		"javascript:alert(1)": "/home",
		"/ok\nSet-Cookie: x":  "/home",
	} {
		if got := SafeRedirect(in, "/home"); got != want {
			t.Errorf("SafeRedirect(%q) = %q, want %q", in, got, want)
		}
	}
}