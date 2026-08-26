package domain

import (
	"errors"
	"fmt"
)

var (
	ErrValidation      = errors.New("validation failed")
	ErrNotFound        = errors.New("not found")
	ErrConflict        = errors.New("conflict")
	ErrUnauthorized    = errors.New("unauthorized")
	ErrForbidden       = errors.New("forbidden")
	ErrExpired         = errors.New("expired")
	ErrUnavailable     = errors.New("unavailable")
	ErrInvalidState    = errors.New("invalid state transition")
	ErrCapacity        = errors.New("capacity exceeded")
	ErrRiskBlocked     = errors.New("risk blocks operation")
	ErrPendingSafety   = errors.New("safety event remains pending")
	ErrVersionConflict = errors.New("version conflict")
)

type FieldError struct {
	Field   string
	Message string
}

func (e FieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func (e FieldError) Unwrap() error { return ErrValidation }

type OpError struct {
	Op       string
	Resource string
	ID       int64
	Err      error
}

func (e *OpError) Error() string {
	if e.ID > 0 {
		return fmt.Sprintf("%s %s %d: %v", e.Op, e.Resource, e.ID, e.Err)
	}
	return fmt.Sprintf("%s %s: %v", e.Op, e.Resource, e.Err)
}

func (e *OpError) Unwrap() error { return e.Err }

func Validation(field, message string) error {
	return FieldError{Field: field, Message: message}
}

func Wrap(op, resource string, id int64, err error) error {
	if err == nil {
		return nil
	}
	return &OpError{Op: op, Resource: resource, ID: id, Err: err}
}

func PublicCode(err error) string {
	switch {
	case errors.Is(err, ErrValidation):
		return "validation_failed"
	case errors.Is(err, ErrUnauthorized):
		return "unauthorized"
	case errors.Is(err, ErrForbidden):
		return "forbidden"
	case errors.Is(err, ErrNotFound):
		return "not_found"
	case errors.Is(err, ErrVersionConflict):
		return "version_conflict"
	case errors.Is(err, ErrCapacity):
		return "capacity_exceeded"
	case errors.Is(err, ErrRiskBlocked):
		return "risk_blocked"
	case errors.Is(err, ErrPendingSafety):
		return "pending_safety_event"
	case errors.Is(err, ErrConflict), errors.Is(err, ErrInvalidState):
		return "conflict"
	case errors.Is(err, ErrExpired):
		return "expired"
	case errors.Is(err, ErrUnavailable):
		return "unavailable"
	default:
		return "internal_error"
	}
}
