package executor

import (
	"slices"
	"testing"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

func TestBuildArgs(t *testing.T) {
	base := domain.DumpSpec{Container: "pg", Database: "my db;rm -rf /", Host: "localhost", Username: "postgres", Port: 5432}

	db := base
	db.Kind = domain.DumpDatabase
	got, err := buildArgs(db)
	want := []string{"exec", "pg", "pg_dump", "--no-owner", "--disable-triggers", "--format=plain",
		"-d", "my db;rm -rf /", "--host=localhost", "-p", "5432", "--username=postgres", "--no-password"}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("got %q err=%v", got, err)
	}

	roles := base
	roles.Kind = domain.DumpRoles
	got, _ = buildArgs(roles)
	want = []string{"exec", "pg", "pg_dumpall", "--host=localhost", "-p", "5432", "--username=postgres", "--no-password", "--roles-only"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}

	if _, err := buildArgs(domain.DumpSpec{Kind: "x"}); err == nil {
		t.Fatal("expected error for unknown kind")
	}
}
func TestRestoreArgs(t *testing.T) {
	got := startEphemeralArgs("pgkeeper-restore-x", "postgres:18")
	want := []string{"run", "--detach", "--rm", "--name", "pgkeeper-restore-x", "--network", "none",
		"--label", "pgkeeper.restore=true", "--env", "POSTGRES_HOST_AUTH_METHOD=trust", "postgres:18"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}

	tgt := domain.SQLTarget{Container: "pg", Host: "localhost", Port: 5432, Username: "postgres", Database: "my db;rm -rf /"}
	got = psqlArgs(tgt)
	want = []string{"exec", "-i", "pg", "psql", "-X", "-q", "-v", "ON_ERROR_STOP=1", "--no-password",
		"--host=localhost", "-p", "5432", "--username=postgres", "-d", "my db;rm -rf /"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q", got)
	}
}
