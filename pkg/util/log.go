package util

import (
	"io"
	"os"

	"github.com/sirupsen/logrus"
)

// Logger is the global logger instance
var Logger = logrus.New()

func init() {
	Logger.SetOutput(os.Stderr)
	Logger.SetLevel(logrus.InfoLevel)
	Logger.SetFormatter(&logrus.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: "2006-01-02 15:04:05",
	})
}

// SetLogLevel sets the logging level
func SetLogLevel(level string) error {
	lvl, err := logrus.ParseLevel(level)
	if err != nil {
		return err
	}
	Logger.SetLevel(lvl)
	return nil
}

// SetLogOutput sets the log output destination
func SetLogOutput(w io.Writer) {
	Logger.SetOutput(w)
}

// SetJSONFormat enables JSON log format
func SetJSONFormat() {
	Logger.SetFormatter(&logrus.JSONFormatter{
		TimestampFormat: "2006-01-02T15:04:05Z07:00",
	})
}

// WithField returns a logger with a field
func WithField(key string, value interface{}) *logrus.Entry {
	return Logger.WithField(key, value)
}

// WithFields returns a logger with multiple fields
func WithFields(fields map[string]interface{}) *logrus.Entry {
	return Logger.WithFields(fields)
}

// WithDevice returns a logger with device context
func WithDevice(device string) *logrus.Entry {
	return Logger.WithField("device", device)
}

// WithOperation returns a logger with operation context
func WithOperation(operation string) *logrus.Entry {
	return Logger.WithField("operation", operation)
}
