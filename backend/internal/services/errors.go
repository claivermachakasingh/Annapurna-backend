package services

import (
	"fmt"
)

type NotFoundError struct {
	Resource string
	ID       string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s not found: %s", e.Resource, e.ID)
}

// ValidationError marks a client-supplied value as invalid. Handlers map it to
// HTTP 422 with the contract's VALIDATION_ERROR code; without it a validation
// failure would fall through to a 500.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Message
	}
	return e.Field + ": " + e.Message
}

type ConflictError struct {
	Code    string
	Message string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

type ForbiddenError struct {
	Message string
}

func (e *ForbiddenError) Error() string {
	return e.Message
}
