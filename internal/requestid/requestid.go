package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

type key struct{}

func New() string {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "request-unavailable"
	}
	return hex.EncodeToString(buffer)
}
func With(ctx context.Context, value string) context.Context {
	return context.WithValue(ctx, key{}, value)
}
func From(ctx context.Context) string { value, _ := ctx.Value(key{}).(string); return value }
