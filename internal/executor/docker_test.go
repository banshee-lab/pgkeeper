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

func TestBuildArgsSnapshot(t *testing.T) {
	db := domain.DumpSpec{Kind: domain.DumpDatabase, Container: "pg", Database: "app", Host: "localhost", Username: "postgres", Port: 5432, SnapshotID: "00000003-0000001B-1"}
	got, err := buildArgs(db)
	if err != nil || got[len(got)-1] != "--snapshot=00000003-0000001B-1" {
		t.Fatalf("got %q err=%v", got, err)
	}
	roles := db
	roles.Kind = domain.DumpRoles
	if got, _ := buildArgs(roles); slices.ContainsFunc(got, func(s string) bool { return len(s) > 10 && s[:10] == "--snapshot" }) {
		t.Fatalf("roles dump must not use a snapshot: %q", got)
	}
	_, args, err := pgDumpToolArgs(db)
	if err != nil || args[len(args)-1] != "--snapshot=00000003-0000001B-1" {
		t.Fatalf("tools args %q err=%v", args, err)
	}
}

func TestSnapshotWriterFindsIDAcrossChunks(t *testing.T) {
	w := &snapshotWriter{found: make(chan string, 1)}
	for _, chunk := range []string{"BEG", "IN\n0000000", "3-0000001B-1\nmore\n"} {
		if n, err := w.Write([]byte(chunk)); err != nil || n != len(chunk) {
			t.Fatal(n, err)
		}
	}
	select {
	case id := <-w.found:
		if id != "00000003-0000001B-1" {
			t.Fatalf("id = %q", id)
		}
	default:
		t.Fatal("snapshot id not found")
	}
}
