package util

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
)

// saveLoggerState saves the current logger state for restoration
func saveLoggerState() (io.Writer, logrus.Level, logrus.Formatter) {
	return Logger.Out, Logger.Level, Logger.Formatter
}

// restoreLoggerState restores the logger to its previous state
func restoreLoggerState(out io.Writer, level logrus.Level, formatter logrus.Formatter) {
	Logger.SetOutput(out)
	Logger.SetLevel(level)
	Logger.SetFormatter(formatter)
}

func TestSetLogLevel(t *testing.T) {
	out, level, formatter := saveLoggerState()
	defer restoreLoggerState(out, level, formatter)

	tests := []struct {
		level   string
		wantErr bool
	}{
		{"debug", false},
		{"info", false},
		{"warn", false},
		{"warning", false},
		{"error", false},
		{"fatal", false},
		{"panic", false},
		{"invalid", true},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			err := SetLogLevel(tt.level)
			if (err != nil) != tt.wantErr {
				t.Errorf("SetLogLevel(%q) error = %v, wantErr %v", tt.level, err, tt.wantErr)
			}
		})
	}
}

func TestSetLogOutput(t *testing.T) {
	out, level, formatter := saveLoggerState()
	defer restoreLoggerState(out, level, formatter)

	var buf bytes.Buffer
	SetLogOutput(&buf)

	// Log something
	Info("test message")

	// Check output was written to buffer
	if buf.Len() == 0 {
		t.Error("Expected output to be written to buffer")
	}
}

func TestSetJSONFormat(t *testing.T) {
	out, level, formatter := saveLoggerState()
	defer restoreLoggerState(out, level, formatter)

	var buf bytes.Buffer
	SetLogOutput(&buf)

	// Enable JSON format
	SetJSONFormat()

	// Log something
	Info("test json")

	// Check output contains JSON markers
	output := buf.String()
	if len(output) == 0 {
		t.Error("Expected output")
	}
	// JSON format should contain { } characters
	if output[0] != '{' {
		t.Errorf("Expected JSON output starting with '{', got: %s", output)
	}
}

func TestWithField(t *testing.T) {
	entry := WithField("key", "value")
	if entry == nil {
		t.Error("WithField should return non-nil entry")
	}
}

func TestWithFields(t *testing.T) {
	entry := WithFields(map[string]interface{}{
		"key1": "value1",
		"key2": 123,
	})
	if entry == nil {
		t.Error("WithFields should return non-nil entry")
	}
}

func TestWithDevice(t *testing.T) {
	entry := WithDevice("leaf1-ny")
	if entry == nil {
		t.Error("WithDevice should return non-nil entry")
	}
}

func TestWithOperation(t *testing.T) {
	entry := WithOperation("service.apply")
	if entry == nil {
		t.Error("WithOperation should return non-nil entry")
	}
}

func TestDebug(t *testing.T) {
	out, level, formatter := saveLoggerState()
	defer restoreLoggerState(out, level, formatter)

	var buf bytes.Buffer
	SetLogOutput(&buf)
	SetLogLevel("debug")

	Debug("debug message")

	if buf.Len() == 0 {
		t.Error("Expected debug output")
	}
}

func TestDebugf(t *testing.T) {
	out, level, formatter := saveLoggerState()
	defer restoreLoggerState(out, level, formatter)

	var buf bytes.Buffer
	SetLogOutput(&buf)
	SetLogLevel("debug")

	Debugf("debug %s %d", "message", 123)

	if buf.Len() == 0 {
		t.Error("Expected debug output")
	}
}

func TestInfo(t *testing.T) {
	out, level, formatter := saveLoggerState()
	defer restoreLoggerState(out, level, formatter)

	var buf bytes.Buffer
	SetLogOutput(&buf)

	Info("info message")

	if buf.Len() == 0 {
		t.Error("Expected info output")
	}
}

func TestInfof(t *testing.T) {
	out, level, formatter := saveLoggerState()
	defer restoreLoggerState(out, level, formatter)

	var buf bytes.Buffer
	SetLogOutput(&buf)

	Infof("info %s %d", "message", 456)

	if buf.Len() == 0 {
		t.Error("Expected info output")
	}
}

func TestWarn(t *testing.T) {
	out, level, formatter := saveLoggerState()
	defer restoreLoggerState(out, level, formatter)

	var buf bytes.Buffer
	SetLogOutput(&buf)

	Warn("warn message")

	if buf.Len() == 0 {
		t.Error("Expected warn output")
	}
}

func TestWarnf(t *testing.T) {
	out, level, formatter := saveLoggerState()
	defer restoreLoggerState(out, level, formatter)

	var buf bytes.Buffer
	SetLogOutput(&buf)

	Warnf("warn %s %d", "message", 789)

	if buf.Len() == 0 {
		t.Error("Expected warn output")
	}
}

func TestError(t *testing.T) {
	out, level, formatter := saveLoggerState()
	defer restoreLoggerState(out, level, formatter)

	var buf bytes.Buffer
	SetLogOutput(&buf)

	Error("error message")

	if buf.Len() == 0 {
		t.Error("Expected error output")
	}
}

func TestErrorf(t *testing.T) {
	out, level, formatter := saveLoggerState()
	defer restoreLoggerState(out, level, formatter)

	var buf bytes.Buffer
	SetLogOutput(&buf)

	Errorf("error %s %d", "message", 999)

	if buf.Len() == 0 {
		t.Error("Expected error output")
	}
}

// Note: Fatal and Fatalf are not tested because they call os.Exit(1)
// which would terminate the test process. They are simple wrappers
// around logrus.Fatal/Fatalf, so we trust the underlying implementation.
// To get coverage, we acknowledge they exist but cannot safely test them.
var _ = Fatal  // Reference to prevent "unused" warning in coverage
var _ = Fatalf // Reference to prevent "unused" warning in coverage
var _ = os.Stderr // Used in init()
