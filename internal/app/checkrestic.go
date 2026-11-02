package app

import (
	"context"
	"fmt"
	"io"

	"github.com/banshee-lab/pgkeeper/internal/config"
	"github.com/banshee-lab/pgkeeper/internal/restic"
)

// CheckRestic implements `pgkeeper -check-restic`: restic version plus
// `restic cat config` for every distinct repository. It never runs init.
func CheckRestic(ctx context.Context, cfg *config.Config, w io.Writer) error {
	rc := restic.New(cfg.Global.TempPath)
	if v, err := rc.CheckVersion(ctx); err != nil {
		fmt.Fprintf(w, "WARN    restic version: %v\n", err)
	} else {
		fmt.Fprintf(w, "OK      restic %s\n", v)
	}

	seen := map[string]bool{}
	failed := 0
	for _, b := range cfg.Backups {
		repo := b.Restic.Repository
		if seen[repo] {
			continue
		}
		seen[repo] = true
		exists, err := rc.Check(ctx, b.Restic)
		switch {
		case err != nil:
			failed++
			fmt.Fprintf(w, "FAIL    %s: %v\n", restic.RedactRepo(repo), err)
		case !exists:
			fmt.Fprintf(w, "MISSING %s (will be initialised on first backup)\n", restic.RedactRepo(repo))
		default:
			fmt.Fprintf(w, "OK      %s\n", restic.RedactRepo(repo))
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d restic repositories failed the check", failed)
	}
	return nil
}