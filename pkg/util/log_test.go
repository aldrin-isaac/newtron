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
	Logger.Info("test message")

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
	Logger.Info("test json")

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

func TestLoggerDirect(t *testing.T) {
	out, level, formatter := saveLoggerState()
	defer restoreLoggerState(out, level, formatter)

	var buf bytes.Buffer
	SetLogOutput(&buf)
	SetLogLevel("debug")

	Logger.Debug("debug message")
	Logger.Debugf("debug %s %d", "message", 123)
	Logger.Info("info message")
	Logger.Infof("info %s %d", "message", 456)
	Logger.Warn("warn message")
	Logger.Warnf("warn %s %d", "message", 789)
	Logger.Error("error message")
	Logger.Errorf("error %s %d", "message", 999)

	if buf.Len() == 0 {
		t.Error("Expected output from Logger direct calls")
	}
}

var _ = os.Stderr // Used in init()
