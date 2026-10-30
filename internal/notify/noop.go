package notify

import (
	"context"
	"errors"
	"log/slog"

	"github.com/banshee-lab/pgkeeper/internal/domain"
)

// Noop is used when [telegram] is absent.
type Noop struct{}

func (Noop) NotifyFailure(_ context.Context, e domain.NotifyEvent) error {
	slog.Debug("notifications disabled; failure not sent",
		"backup", e.Backup.Name, "activity", e.Activity.ID)
	return nil
}

// Multi fans out to several channels; all are attempted.
type Multi []domain.Notifier

func (m Multi) NotifyFailure(ctx context.Context, e domain.NotifyEvent) error {
	var errs []error
	for _, n := range m {
		if err := n.NotifyFailure(ctx, e); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

var (
	_ domain.Notifier = Noop{}
	_ domain.Notifier = Multi(nil)
)