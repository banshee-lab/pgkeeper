package restic

import (
	"context"
	"io"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

// Noop is the phase-3 stand-in; replaced by the real client in phase 4.
type Noop struct{}

var _ domain.ResticClient = Noop{}

func (Noop) EnsureRepo(context.Context, domain.ResticConfig, io.Writer) error { return nil }

func (Noop) Backup(_ context.Context, _ domain.ResticConfig, tag, host, dir string, files []string, logw io.Writer) error {
	if l, ok := logw.(interface{ Logf(level, format string, args ...any) }); ok {
		l.Logf("INFO", "restic disabled (phase 4)")
	}
	return nil
}

func (Noop) Forget(context.Context, domain.ResticConfig, string, string, io.Writer) error { return nil }