// Package util provides utility functions and common error types.
package util

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors for precondition failures
var (
	ErrNotConnected          = errors.New("device not connected")
	ErrPermissionDenied      = errors.New("permission denied")
	ErrPreconditionFailed    = errors.New("precondition not met")
	ErrValidationFailed      = errors.New("validation failed")
	ErrDeviceLocked          = errors.New("device is locked by another process")
	ErrDeviceZombieIntent = errors.New("device has a zombie operation from a crashed process — inspect with 'device zombie', then rollback or clear before proceeding")
	ErrConflict              = errors.New("conflict: referenced by other entities")
)

// ConflictError indicates a requested mutation would violate an invariant
// because of references from other entities. Examples: deleting a topology
// device that has links still wired to it; deleting a profile that one or
// more topology devices still reference. Operators resolve by removing the
// referring entities first, or by passing force=true to cascade-delete them
// along with the target (per DESIGN_PRINCIPLES §15 operational symmetry:
// cascade is explicit, never implicit).
//
// References names the entities that block the mutation — operator can read
// them off the error and act on each.
type ConflictError struct {
	Resource   string   // resource kind being deleted ("topology-device", "profile")
	Name       string   // its name
	References []string // referring entity descriptions, human-readable
}

func (e *ConflictError) Error() string {
	if len(e.References) == 1 {
		return fmt.Sprintf("%s '%s' has 1 reference: %s — pass force=true to cascade",
			e.Resource, e.Name, e.References[0])
	}
	return fmt.Sprintf("%s '%s' has %d references: %s — pass force=true to cascade",
		e.Resource, e.Name, len(e.References), strings.Join(e.References, ", "))
}

func (e *ConflictError) Unwrap() error {
	return ErrConflict
}

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

// Add adds an error message if condition is false. Used by the
// spec loader to express required-field checks as a fluent chain.
func (v *ValidationBuilder) Add(condition bool, message string) *ValidationBuilder {
	if !condition {
		v.errors = append(v.errors, message)
	}
	return v
}

// AddErrorf adds a formatted error message
func (v *ValidationBuilder) AddErrorf(format string, args ...interface{}) *ValidationBuilder {
	v.errors = append(v.errors, fmt.Sprintf(format, args...))
	return v
}

// Build returns the validation error or nil if no errors
func (v *ValidationBuilder) Build() error {
	if len(v.errors) == 0 {
		return nil
	}
	return &ValidationError{Errors: v.errors}
}

