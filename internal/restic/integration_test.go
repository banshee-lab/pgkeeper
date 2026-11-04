//go:build integration

package restic

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

type snapshot struct {
	ID    string   `json:"id"`
	Host  string   `json:"hostname"`
	Tags  []string `json:"tags"`
	Paths []string `json:"paths"`
}

func resticOut(t *testing.T, cfg domain.ResticConfig, args ...string) []byte {
	t.Helper()
	cmd := exec.Command("restic", args...)
	cmd.Env = buildEnv(cfg, filepath.Join(t.TempDir(), "cache"), os.Environ())
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("restic %v: %v", args, err)
	}
	return out
}

func snapshots(t *testing.T, cfg domain.ResticConfig) []snapshot {
	t.Helper()
	var s []snapshot
	if err := json.Unmarshal(resticOut(t, cfg, "snapshots", "--json"), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestIntegrationBackupRetentionRestore(t *testing.T) {
	if _, err := exec.LookPath("restic"); err != nil {
		t.Skip("restic not on PATH")
	}
	ctx := context.Background()
	cfg := domain.ResticConfig{Repository: filepath.Join(t.TempDir(), "repo"), Password: "pw-test", KeepDaily: 1}
	c := New(t.TempDir())
	var lg bytes.Buffer

	if err := c.EnsureRepo(ctx, cfg, &lg); err != nil {
		t.Fatalf("ensure: %v\n%s", err, lg.String())
	}
	if err := c.EnsureRepo(ctx, cfg, &lg); err != nil { // second call must not re-init
		t.Fatal(err)
	}

	work := t.TempDir()
	files := []string{"app.sql", "app_roles.sql"}
	for i := 0; i < 3; i++ {
		_ = os.WriteFile(filepath.Join(work, "app.sql"), []byte("SELECT "+string(rune('1'+i))+";\n"), 0o600)
		_ = os.WriteFile(filepath.Join(work, "app_roles.sql"), []byte("CREATE ROLE x;\n"), 0o600)
		snap, err := c.Backup(ctx, cfg, "a", "a", work, files, &lg)
		if err != nil {
			t.Fatalf("backup: %v\n%s", err, lg.String())
		}
		if snap == nil || snap.ID == "" || snap.FilesAdded+snap.FilesChanged == 0 {
			t.Fatalf("backup %d: missing or empty snapshot summary: %+v\n%s", i, snap, lg.String())
		}
	}
	if _, err := c.Backup(ctx, cfg, "b", "b", work, files[:1], &lg); err != nil {
		t.Fatal(err)
	}
	if n := len(snapshots(t, cfg)); n != 4 {
		t.Fatalf("expected 4 snapshots before forget, got %d", n)
	}

	if err := c.Forget(ctx, cfg, "a", "a", &lg); err != nil {
		t.Fatalf("forget: %v\n%s", err, lg.String())
	}
	snaps := snapshots(t, cfg)
	if len(snaps) != 2 {
		t.Fatalf("expected 2 snapshots after forget (1 for a, 1 for b), got %d: %+v", len(snaps), snaps)
	}
	var a *snapshot
	for i := range snaps {
		if slices.Contains(snaps[i].Tags, "a") {
			a = &snaps[i]
		}
	}
	if a == nil || a.Host != "a" || len(a.Paths) == 0 {
		t.Fatalf("snapshot for tag a missing or wrong: %+v", snaps)
	}

	target := t.TempDir()
	resticOut(t, cfg, "restore", a.ID, "--target", target)
	got, err := os.ReadFile(filepath.Join(target, work, "app.sql"))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := os.ReadFile(filepath.Join(work, "app.sql"))
	if !bytes.Equal(got, want) {
		t.Fatalf("restored %q, want %q", got, want)
	}
}