package pebblestore

import "github.com/rs/zerolog"

// // // // // // // // // //

type zeroLoggerObj struct {
	logObj zerolog.Logger
}

// // // // // // // // // //

// NewLogger creates a logger for Pebble EventListener and EnsureDefaults.
func NewLogger(logObj zerolog.Logger) *zeroLoggerObj {
	componentLogObj := logObj.With().Str("component", "pebble").Logger()
	return &zeroLoggerObj{logObj: componentLogObj}
}

func (obj *zeroLoggerObj) Infof(format string, args ...interface{}) {
	obj.logObj.Debug().Msgf(format, args...)
}

func (obj *zeroLoggerObj) Errorf(format string, args ...interface{}) {
	obj.logObj.Error().Msgf(format, args...)
}

func (obj *zeroLoggerObj) Fatalf(format string, args ...interface{}) {
	obj.logObj.Fatal().Msgf(format, args...)
}
