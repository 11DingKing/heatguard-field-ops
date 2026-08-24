package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/11DingKing/heatguard-field-ops/internal/domain"
	"github.com/11DingKing/heatguard-field-ops/internal/requestid"
)

type errorBody struct {
	Error apiError `json:"error"`
}
type apiError struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return domain.Validation("body", err.Error())
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return domain.Validation("body", "must contain one JSON value")
	}
	return nil
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	message := "internal server error"
	switch {
	case errors.Is(err, domain.ErrValidation):
		status = http.StatusBadRequest
		message = err.Error()
	case errors.Is(err, domain.ErrUnauthorized):
		status = http.StatusUnauthorized
		message = "authentication required"
	case errors.Is(err, domain.ErrForbidden):
		status = http.StatusForbidden
		message = "permission denied"
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
		message = "resource not found"
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrInvalidState), errors.Is(err, domain.ErrVersionConflict), errors.Is(err, domain.ErrCapacity), errors.Is(err, domain.ErrRiskBlocked), errors.Is(err, domain.ErrPendingSafety):
		status = http.StatusConflict
		message = err.Error()
	case errors.Is(err, domain.ErrUnavailable):
		status = http.StatusServiceUnavailable
		message = "service unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		status = http.StatusGatewayTimeout
		message = "request deadline exceeded"
	}
	writeJSON(w, status, errorBody{Error: apiError{Code: domain.PublicCode(err), Message: message, RequestID: requestid.From(r.Context())}})
}
