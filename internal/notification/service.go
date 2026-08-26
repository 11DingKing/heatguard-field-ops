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
	message := fmt.Sprintf("HeatGuard safety alert %d", alertID)
	if !fresh {
		message += " (replay)"
	}
	_, err = s.sender.Send(ctx, contact, message)
	if err != nil {
		_ = s.store.MergeNotificationReceipt(ctx, providerKey, "failed", s.now().UTC())
		return fmt.Errorf("send notification: %w", err)
	}
	return s.store.MergeNotificationReceipt(ctx, providerKey, "sent", s.now().UTC())
}

func (s *Service) Receipt(ctx context.Context, providerKey, status string, at time.Time) error {
	return s.store.MergeNotificationReceipt(ctx, providerKey, status, at.UTC())
}
