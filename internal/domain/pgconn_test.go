package domain

import (
	"slices"
	"strings"
	"testing"
)

func TestParsePGURL(t *testing.T) {
	c, err := ParsePGURL("postgresql://app:p%40ss@db.example.com:6543/orders?sslmode=require&connect_timeout=5")
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "db.example.com" || c.Port != 6543 || c.User != "app" || c.Password.Reveal() != "p@ss" || c.Database != "orders" {
		t.Fatalf("unexpected: %+v", c)
	}
	want := []string{"PGHOST=db.example.com", "PGPORT=6543", "PGUSER=app", "PGPASSWORD=p@ss", "PGDATABASE=orders",
		"PGCONNECT_TIMEOUT=5", "PGSSLMODE=require"}
	if got := c.Env(); !slices.Equal(got, want) {
		t.Fatalf("env = %q, want %q", got, want)
	}
	if got := c.Redacted(); got != "postgresql://app@db.example.com:6543/orders" {
		t.Fatalf("redacted = %q", got)
	}
}

func TestParsePGURLDefaults(t *testing.T) {
	c, err := ParsePGURL("postgres://db.internal")
	if err != nil {
		t.Fatal(err)
	}
	if c.Host != "db.internal" || c.Port != 5432 || c.User != "postgres" || c.Database != "" || c.Password != "" {
		t.Fatalf("unexpected: %+v", c)
	}
	for _, kv := range c.Env() {
		if strings.HasPrefix(kv, "PGPASSWORD=") || strings.HasPrefix(kv, "PGDATABASE=") {
			t.Errorf("unexpected %s", kv)
		}
	}
}

func TestParsePGURLErrors(t *testing.T) {
	for name, tc := range map[string]struct{ url, want string }{
		"scheme":      {"mysql://u:hunter2@h/db", "scheme"},
		"no host":     {"postgresql:///db", "host is required"},
		"multi host":  {"postgresql://h1:5432,h2:5432/db", "multiple hosts"},
		"bad port":    {"postgresql://u:hunter2@h:99999/db", "port"},
		"parse error": {"postgresql://u:hunter2@h:abc/db", "malformed"},
		"param":       {"postgresql://u:hunter2@h/db?sslrootcert=/x", "unsupported parameter"},
		"dup param":   {"postgresql://h/db?sslmode=a&sslmode=b", "more than once"},
		"nested path": {"postgresql://h/a/b", "must not contain '/'"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParsePGURL(tc.url)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Fatalf("error leaks the password: %v", err)
			}
		})
	}
}
