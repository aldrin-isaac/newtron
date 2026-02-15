package util

import (
	"errors"
	"strings"
	"testing"
)

func TestPreconditionError(t *testing.T) {
	err := NewPreconditionError("delete", "Vlan100", "VLAN must not have members", "has 3 members")

	// Test Error() message
	msg := err.Error()
	if !strings.Contains(msg, "delete") {
		t.Errorf("Error message should contain operation: %s", msg)
	}
	if !strings.Contains(msg, "Vlan100") {
		t.Errorf("Error message should contain resource: %s", msg)
	}
	if !strings.Contains(msg, "VLAN must not have members") {
		t.Errorf("Error message should contain precondition: %s", msg)
	}
	if !strings.Contains(msg, "has 3 members") {
		t.Errorf("Error message should contain details: %s", msg)
	}

	// Test Unwrap
	if !errors.Is(err, ErrPreconditionFailed) {
		t.Errorf("PreconditionError should unwrap to ErrPreconditionFailed")
	}
}

func TestPreconditionErrorNoDetails(t *testing.T) {
	err := NewPreconditionError("create", "VRF", "VRF name required", "")
	msg := err.Error()

	// Should not have trailing parentheses when no details
	if strings.Contains(msg, "()") || strings.HasSuffix(msg, ")") {
		// Check if it's from details
		if !strings.Contains(msg, "(") {
			// No opening paren means no details section - good
		} else if strings.HasSuffix(msg, "()") {
			t.Errorf("Error message should not have empty details: %s", msg)
		}
	}
}

func TestValidationError(t *testing.T) {
	t.Run("single error", func(t *testing.T) {
		err := NewValidationError("field is required")
		msg := err.Error()
		if !strings.Contains(msg, "field is required") {
			t.Errorf("Error message should contain the error: %s", msg)
		}
		if !errors.Is(err, ErrValidationFailed) {
			t.Errorf("ValidationError should unwrap to ErrValidationFailed")
		}
	})

	t.Run("multiple errors", func(t *testing.T) {
		err := NewValidationError("field1 is required", "field2 is invalid", "field3 out of range")
		msg := err.Error()
		if !strings.Contains(msg, "field1") || !strings.Contains(msg, "field2") || !strings.Contains(msg, "field3") {
			t.Errorf("Error message should contain all errors: %s", msg)
		}
	})
}

func TestValidationBuilder(t *testing.T) {
	t.Run("no errors", func(t *testing.T) {
		v := &ValidationBuilder{}
		v.Add(true, "this should not appear")
		v.Add(true, "neither should this")

		if v.HasErrors() {
			t.Error("Should not have errors when all conditions are true")
		}
		if err := v.Build(); err != nil {
			t.Errorf("Build() should return nil when no errors: %v", err)
		}
	})

	t.Run("with errors", func(t *testing.T) {
		v := &ValidationBuilder{}
		v.Add(false, "first error")
		v.Add(true, "this passes")
		v.Add(false, "second error")
		v.AddError("unconditional error")
		v.AddErrorf("formatted error: %d", 42)

		if !v.HasErrors() {
			t.Error("Should have errors")
		}

		err := v.Build()
		if err == nil {
			t.Fatal("Build() should return error")
		}

		validationErr, ok := err.(*ValidationError)
		if !ok {
			t.Fatalf("Expected *ValidationError, got %T", err)
		}
		if len(validationErr.Errors) != 4 {
			t.Errorf("Expected 4 errors, got %d", len(validationErr.Errors))
		}
	})

	t.Run("chaining", func(t *testing.T) {
		err := (&ValidationBuilder{}).
			Add(false, "error1").
			Add(false, "error2").
			AddErrorf("error%d", 3).
			Build()

		if err == nil {
			t.Fatal("Expected error")
		}
		if !strings.Contains(err.Error(), "error1") {
			t.Errorf("Missing error1 in: %s", err.Error())
		}
	})
}

func TestSentinelErrors(t *testing.T) {
	// Test that sentinel errors are distinct
	sentinels := []error{
		ErrNotConnected,
		ErrPermissionDenied,
		ErrPreconditionFailed,
		ErrValidationFailed,
		ErrDeviceLocked,
	}

	for i, err1 := range sentinels {
		for j, err2 := range sentinels {
			if i != j && errors.Is(err1, err2) {
				t.Errorf("Sentinel errors should be distinct: %v == %v", err1, err2)
			}
		}
	}
}

func TestErrorsIsWrapping(t *testing.T) {
	// Test that errors.Is works with wrapped errors
	tests := []struct {
		name     string
		err      error
		sentinel error
	}{
		{"PreconditionError", NewPreconditionError("op", "res", "pre", ""), ErrPreconditionFailed},
		{"ValidationError", NewValidationError("msg"), ErrValidationFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.err, tt.sentinel) {
				t.Errorf("%s should wrap %v", tt.name, tt.sentinel)
			}
		})
	}
}
