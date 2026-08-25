package notification

import (
	"context"
	"fmt"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/repository"
)

type Sender interface {
	Send(context.Context, string, string) (string, error)
}

type Service struct {
	store  repository.Store
	sender Sender
	now    func() time.Time
}

func New(store repository.Store, sender Sender, now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{store: store, sender: sender, now: now}
}

func (s *Service) Notify(ctx context.Context, alertID int64, contact, channel string) error {
	providerKey := fmt.Sprintf("alert-%d-%s-%s", alertID, channel, contact)
	_, fresh, err := s.store.InsertNotificationAttempt(ctx, alertID, contact, channel, providerKey, s.now().UTC())
	if err != nil {
		return err
	}
	if !fresh {
		return nil
	}
	_, err = s.sender.Send(ctx, contact, fmt.Sprintf("HeatGuard safety alert %d", alertID))
	if err != nil {
		_ = s.store.MergeNotificationReceipt(ctx, providerKey, "failed", s.now().UTC())
		return fmt.Errorf("send notification: %w", err)
	}
	return s.store.MergeNotificationReceipt(ctx, providerKey, "sent", s.now().UTC())
}

func (s *Service) Receipt(ctx context.Context, providerKey, status string, at time.Time) error {
	// Forward the supplier's event time as-is; the store guards against receipts
	// that would regress a confirmed delivery or backdate its recorded event time,
	// which is what happens when a delayed out-of-order receipt arrives after the
	// network recovers. Clamping to "now" here would let a late failed receipt
	// overwrite the confirmed delivery time with its arrival moment.
	return s.store.MergeNotificationReceipt(ctx, providerKey, status, at.UTC())
}
