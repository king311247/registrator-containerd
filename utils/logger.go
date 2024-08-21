package utils

import (
	"context"
	"github.com/sirupsen/logrus"
)

var G = GetLogger

type Entry = logrus.Entry

var L = &Entry{
	Logger: logrus.StandardLogger(),
	// Default is three fields plus a little extra room.
	Data: make(Fields, 6),
}

type Fields = map[string]any
type loggerKey struct{}

func GetLogger(ctx context.Context) *Entry {
	if logger := ctx.Value(loggerKey{}); logger != nil {
		return logger.(*Entry)
	}
	return L.WithContext(ctx)
}
