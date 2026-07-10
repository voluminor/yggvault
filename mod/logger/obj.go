package logger

import (
	"errors"
	"io"
	"sync"

	"github.com/rs/zerolog"
)

// // // // // // // // // //

const cMegabyte = 1000 * 1000

// Obj is the assembled logger: zerolog value, closeable sinks, and optional VictoriaLogs sink reference.
type Obj struct {
	value     zerolog.Logger
	closerObj *closerObj
	vlSink    *victorialogsWriterObj
}

type closerObj struct {
	once      sync.Once
	closerArr []io.Closer
	err       error
}

// //

// Close closes all sinks once and joins their errors; repeated calls return the same result.
func (obj *closerObj) Close() error {
	if obj == nil {
		return nil
	}

	obj.once.Do(func() {
		errArr := make([]error, 0, len(obj.closerArr))
		for _, itemObj := range obj.closerArr {
			if itemObj == nil {
				continue
			}
			if err := itemObj.Close(); err != nil {
				errArr = append(errArr, err)
			}
		}
		obj.err = errors.Join(errArr...)
	})

	return obj.err
}
