// Package util provides utility functions and common error types.
package util

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for precondition failures
var (
	ErrNotConnected       = errors.New("device not connected")
	ErrPermissionDenied   = errors.New("permission denied")
	ErrPreconditionFailed = errors.New("precondition not met")
	ErrValidationFailed   = errors.New("validation failed")
	ErrDeviceLocked       = errors.New("device is locked by another process")
)

// PreconditionError represents a failed precondition check with context
type PreconditionError struct {
	Operation    string
	Resource     string
	Precondition string
	Details      string
}

func (e *PreconditionError) Error() string {
	msg := fmt.Sprintf("precondition failed for %s on %s: %s", e.Operation, e.Resource, e.Precondition)
	if e.Details != "" {
		msg += " (" + e.Details + ")"
	}
	return msg
}

func (e *PreconditionError) Unwrap() error {
	return ErrPreconditionFailed
}

// NewPreconditionError creates a new precondition error
func NewPreconditionError(operation, resource, precondition, details string) *PreconditionError {
	return &PreconditionError{
		Operation:    operation,
		Resource:     resource,
		Precondition: precondition,
		Details:      details,
	}
}

// ValidationError represents one or more validation failures
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	if len(e.Errors) == 1 {
		return "validation failed: " + e.Errors[0]
	}
	return fmt.Sprintf("validation failed:\n  - %s", strings.Join(e.Errors, "\n  - "))
}

func (e *ValidationError) Unwrap() error {
	return ErrValidationFailed
}

// NewValidationError creates a validation error from messages
func NewValidationError(messages ...string) *ValidationError {
	return &ValidationError{Errors: messages}
}

// ValidationBuilder helps accumulate validation errors
type ValidationBuilder struct {
	errors []string
}

// Add adds an error message if condition is false
func (v *ValidationBuilder) Add(condition bool, message string) *ValidationBuilder {
	if !condition {
		v.errors = append(v.errors, message)
	}
	return v
}

// AddError adds an error message unconditionally
func (v *ValidationBuilder) AddError(message string) *ValidationBuilder {
	v.errors = append(v.errors, message)
	return v
}

// AddErrorf adds a formatted error message
func (v *ValidationBuilder) AddErrorf(format string, args ...interface{}) *ValidationBuilder {
	v.errors = append(v.errors, fmt.Sprintf(format, args...))
	return v
}

// HasErrors returns true if there are validation errors
func (v *ValidationBuilder) HasErrors() bool {
	return len(v.errors) > 0
}

// Build returns the validation error or nil if no errors
func (v *ValidationBuilder) Build() error {
	if len(v.errors) == 0 {
		return nil
	}
	return &ValidationError{Errors: v.errors}
}

