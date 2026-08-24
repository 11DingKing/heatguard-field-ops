package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/repository"
)

type Service struct{ now func() time.Time }

func New(now func() time.Time) *Service {
	if now == nil {
		now = time.Now
	}
	return &Service{now: now}
}

func (s *Service) Record(ctx context.Context, writer repository.Writer, actor int64, action, objectType string, objectID int64, result, requestID string, metadata any) error {
	payload, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	event := domain.AuditEvent{ActorID: &actor, Action: action, ObjectType: objectType, ObjectID: strconv.FormatInt(objectID, 10), Result: result, RequestID: requestID, Metadata: string(payload), CreatedAt: s.now().UTC()}
	return writer.InsertAudit(ctx, &event)
}
