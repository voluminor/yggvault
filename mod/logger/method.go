package logger

import (
	"errors"

	"github.com/voluminor/yggvault/target/stcode"

	"github.com/rs/zerolog"
)

// // // // // // // // // //

// Zero returns the underlying zerolog.Logger for structured logging.
func (obj *Obj) Zero() *zerolog.Logger {
	return &obj.value
}

// Close closes all sinks exactly once and is safe on a nil receiver.
func (obj *Obj) Close() error {
	if obj == nil {
		return nil
	}
	return obj.closerObj.Close()
}

// //

// XErr expands stcode errors into structured events with their fields; other errors use Error.Err.
func (obj *Obj) XErr(err error) *zerolog.Event {
	var xerr stcode.XErrInterface
	if errors.As(err, &xerr) {
		return xerr.GetXErr(obj.value)
	}

	return obj.value.Error().Err(err)
}
