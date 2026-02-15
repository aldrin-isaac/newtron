package util

import (
	"bytes"
	"os"
	"testing"

	"github.com/sirupsen/logrus"
)

// saveLoggerState saves the current logger state for restoration
func saveLoggerState() (*logrus.Logger, logrus.Level, logrus.Formatter) {
	return Logger, Logger.Level, Logger.Formatter
}

// restoreLoggerState restores the logger to its previous state
func restoreLoggerState(saved *logrus.Logger, level logrus.Level, formatter logrus.Formatter) {
	Logger.SetOutput(os.Stderr)
	Logger.SetLevel(level)
	Logger.SetFormatter(formatter)
}

func TestSetLogLevel(t *testing.T) {
	_, level, formatter := saveLoggerState()
	defer restoreLoggerState(nil, level, formatter)

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

func TestWithDevice(t *testing.T) {
	entry := WithDevice("leaf1-ny")
	if entry == nil {
		t.Error("WithDevice should return non-nil entry")
	}
}

func TestLoggerDirect(t *testing.T) {
	_, level, formatter := saveLoggerState()
	defer restoreLoggerState(nil, level, formatter)

	var buf bytes.Buffer
	Logger.SetOutput(&buf)
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
