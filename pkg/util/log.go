package util

import (
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

// WithDevice returns a logger with device context
func WithDevice(device string) *logrus.Entry {
	return Logger.WithField("device", device)
}
