package backup

import (
	"bytes"
	"strings"
	"testing"
)

func TestPrepareRolesGuardsCreateRole(t *testing.T) {
	src := "SET default_transaction_read_only = off;\n" +
		"CREATE ROLE app_user;\n" +
		"ALTER ROLE app_user WITH NOSUPERUSER LOGIN PASSWORD 'x';\n" +
		"CREATE ROLE \"O'Brien\";\n" +
		"ALTER ROLE postgres WITH SUPERUSER;"
	var out bytes.Buffer
	n, err := prepareRoles(strings.NewReader(src), &out)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("roles = %d, want 2", n)
	}
	got := out.String()
	for _, want := range []string{
		"SET default_transaction_read_only = off;\n",
		"IF to_regrole('app_user') IS NULL THEN CREATE ROLE app_user;",
		"ALTER ROLE app_user WITH NOSUPERUSER LOGIN PASSWORD 'x';\n",
		`to_regrole('"O''Brien"') IS NULL THEN CREATE ROLE "O'Brien";`,
		"RAISE NOTICE 'pgkeeper: role % already exists, overwriting its attributes'",
		"ALTER ROLE postgres WITH SUPERUSER;", // last line without a newline is kept
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\nCREATE ROLE app_user;") {
		t.Errorf("bare CREATE ROLE must be rewritten:\n%s", got)
	}
}

func TestPrepareRolesRejectsDollarTag(t *testing.T) {
	_, err := prepareRoles(strings.NewReader("CREATE ROLE \"x$pgkeeper$y\";\n"), &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error")
	}
}
