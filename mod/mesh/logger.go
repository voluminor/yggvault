package mesh

import (
	"fmt"
	"strings"

	"github.com/rs/zerolog"
)

// // // // // // // // // //

type ratatoskrLoggerObj struct {
	logObj zerolog.Logger
}

// //

func newRatatoskrLogger(logObj zerolog.Logger) *ratatoskrLoggerObj {
	componentLogObj := logObj.With().Str("component", "ratatoskr").Logger()
	return &ratatoskrLoggerObj{logObj: componentLogObj}
}

func sprintLine(args ...interface{}) string {
	return strings.TrimRight(fmt.Sprintln(args...), "\r\n")
}

func (obj *ratatoskrLoggerObj) Printf(format string, args ...interface{}) {
	obj.logObj.Debug().Msgf(format, args...)
}

func (obj *ratatoskrLoggerObj) Println(args ...interface{}) {
	obj.logObj.Debug().Msg(sprintLine(args...))
}

func (obj *ratatoskrLoggerObj) Infof(format string, args ...interface{}) {
	obj.logObj.Info().Msgf(format, args...)
}

func (obj *ratatoskrLoggerObj) Infoln(args ...interface{}) {
	obj.logObj.Info().Msg(sprintLine(args...))
}

func (obj *ratatoskrLoggerObj) Warnf(format string, args ...interface{}) {
	obj.logObj.Warn().Msgf(format, args...)
}

func (obj *ratatoskrLoggerObj) Warnln(args ...interface{}) {
	obj.logObj.Warn().Msg(sprintLine(args...))
}

func (obj *ratatoskrLoggerObj) Errorf(format string, args ...interface{}) {
	obj.logObj.Error().Msgf(format, args...)
}

func (obj *ratatoskrLoggerObj) Errorln(args ...interface{}) {
	obj.logObj.Error().Msg(sprintLine(args...))
}

func (obj *ratatoskrLoggerObj) Debugf(format string, args ...interface{}) {
	obj.logObj.Debug().Msgf(format, args...)
}

func (obj *ratatoskrLoggerObj) Debugln(args ...interface{}) {
	obj.logObj.Debug().Msg(sprintLine(args...))
}

func (obj *ratatoskrLoggerObj) Traceln(args ...interface{}) {
	obj.logObj.Trace().Msg(sprintLine(args...))
}
