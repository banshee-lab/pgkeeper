package executor

import (
	"slices"
	"strings"
	"testing"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

func testConn() *domain.PGConn {
	return &domain.PGConn{Host: "db", Port: 5432, User: "u", Password: "hunter2", Database: "app",
		Params: map[string]string{"sslmode": "require"}}
}

func TestToolsRunArgsKeepPasswordOutOfArgs(t *testing.T) {
	conn := testConn()
	got := toolsRunArgs("pgkeeper-tools-x", "postgres:18", conn, "pg_dump", []string{"--format=plain"})
	want := []string{"run", "--rm", "-i", "--name", "pgkeeper-tools-x", "--label", "pgkeeper.restore=true",
		"--entrypoint", "pg_dump",
		"--env", "PGHOST", "--env", "PGPORT", "--env", "PGUSER", "--env", "PGPASSWORD", "--env", "PGDATABASE", "--env", "PGSSLMODE",
		"postgres:18", "--format=plain"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
	for _, a := range got {
		if strings.Contains(a, "hunter2") {
			t.Fatalf("password in arguments: %q", got)
		}
	}
	if !slices.Contains(toolsEnv(conn), "PGPASSWORD=hunter2") {
		t.Fatal("password missing from the docker CLI environment")
	}
}

func TestPgDumpToolArgs(t *testing.T) {
	conn := testConn()
	tool, args, err := pgDumpToolArgs(domain.DumpSpec{Kind: domain.DumpDatabase, Conn: conn})
	if err != nil || tool != "pg_dump" ||
		!slices.Equal(args, []string{"--no-owner", "--disable-triggers", "--format=plain", "--no-password"}) {
		t.Fatalf("got %s %q err=%v", tool, args, err)
	}
	tool, args, err = pgDumpToolArgs(domain.DumpSpec{Kind: domain.DumpRoles, Conn: conn})
	if err != nil || tool != "pg_dumpall" || !slices.Equal(args, []string{"--roles-only", "--no-password", "--database=app"}) {
		t.Fatalf("got %s %q err=%v", tool, args, err)
	}
	if _, _, err := pgDumpToolArgs(domain.DumpSpec{Kind: "x"}); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}

func TestPsqlToolArgs(t *testing.T) {
	got := psqlToolArgs("postgres")
	want := []string{"-X", "-q", "-v", "ON_ERROR_STOP=1", "--no-password", "-d", "postgres"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
}
