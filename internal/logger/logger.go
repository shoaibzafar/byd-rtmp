package logger

import (
	"os"

	"github.com/sirupsen/logrus"
)

// Logger represents a logger instance
type Logger struct {
	*logrus.Logger
}

var defaultLogger *Logger

func init() {
	l := logrus.New()
	l.SetOutput(os.Stdout)
	l.SetLevel(logrus.InfoLevel)
	l.SetFormatter(&logrus.JSONFormatter{})
	defaultLogger = &Logger{l}
}

// SetLevel sets the logging level
func SetLevel(level string) {
	switch level {
	case "debug":
		defaultLogger.SetLevel(logrus.DebugLevel)
	case "info":
		defaultLogger.SetLevel(logrus.InfoLevel)
	case "warn":
		defaultLogger.SetLevel(logrus.WarnLevel)
	case "error":
		defaultLogger.SetLevel(logrus.ErrorLevel)
	case "fatal":
		defaultLogger.SetLevel(logrus.FatalLevel)
	case "panic":
		defaultLogger.SetLevel(logrus.PanicLevel)
	default:
		defaultLogger.SetLevel(logrus.InfoLevel)
	}
}

// SetFormatter sets the logging format
func SetFormatter(format string) {
	switch format {
	case "text":
		defaultLogger.SetFormatter(&logrus.TextFormatter{})
	case "json":
		defaultLogger.SetFormatter(&logrus.JSONFormatter{})
	default:
		defaultLogger.SetFormatter(&logrus.JSONFormatter{})
	}
}

// SetOutput sets the logging output
func SetOutput(file string) {
	if file != "" {
		f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err == nil {
			defaultLogger.SetOutput(f)
		}
	}
}

// GetLogger returns the logger instance
func GetLogger() *Logger {
	return defaultLogger
}

// Debugf logs a debug message with formatting
func (l *Logger) Debugf(format string, args ...interface{}) {
	l.Logger.Debugf(format, args...)
}

// Infof logs an info message with formatting
func (l *Logger) Infof(format string, args ...interface{}) {
	l.Logger.Infof(format, args...)
}

// Warnf logs a warning message with formatting
func (l *Logger) Warnf(format string, args ...interface{}) {
	l.Logger.Warnf(format, args...)
}

// Errorf logs an error message with formatting
func (l *Logger) Errorf(format string, args ...interface{}) {
	l.Logger.Errorf(format, args...)
}

// Debug logs a debug message
func (l *Logger) Debug(args ...interface{}) {
	l.Logger.Debug(args...)
}

// Info logs an info message
func (l *Logger) Info(args ...interface{}) {
	l.Logger.Info(args...)
}

// Warn logs a warning message
func (l *Logger) Warn(args ...interface{}) {
	l.Logger.Warn(args...)
}

// Error logs an error message
func (l *Logger) Error(args ...interface{}) {
	l.Logger.Error(args...)
}
