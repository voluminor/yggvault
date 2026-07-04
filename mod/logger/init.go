package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target"
	stcfg "github.com/voluminor/yggvault/target/stconf"

	"github.com/rs/zerolog"
	"gopkg.in/natefinch/lumberjack.v2"
)

// // // // // // // // // //

func init() {
	zerolog.TimestampFunc = func() time.Time {
		return time.Now().UTC()
	}
	zerolog.TimeFieldFormat = core.TimeFormat
}

// New builds a multiplexed zerolog from enabled sinks; the effective level is the minimum sink level.
// It fails when no sink is enabled.
func New(configObj *stcfg.ConfigObj) (*Obj, error) {
	if configObj == nil {
		return nil, fmt.Errorf("logger config is nil")
	}

	writers := make([]io.Writer, 0, 3)
	levels := make([]zerolog.Level, 0, 3)
	closerArr := make([]io.Closer, 0, 2)
	var vlSinkObj *victorialogsWriterObj

	logConfigObj := configObj.Logging

	if logConfigObj.Console.Enabled {
		level, err := parseLevel(logConfigObj.Console.Level.String())
		if err != nil {
			return nil, err
		}

		consoleWriter := zerolog.NewConsoleWriter(func(w *zerolog.ConsoleWriter) {
			w.Out = os.Stdout
			w.TimeFormat = core.TimeFormat
			w.NoColor = false
		})

		writers = append(writers, &levelWriterObj{writer: consoleWriter, level: level})
		levels = append(levels, level)
	}

	if logConfigObj.File.Enabled {
		if err := checkFileConfig(logConfigObj.File); err != nil {
			return nil, err
		}

		maxSizeMB, err := sizeBytesToMegabytes(logConfigObj.File.MaxSize)
		if err != nil {
			return nil, err
		}
		maxAgeDays, err := durationToDays(logConfigObj.File.MaxAge)
		if err != nil {
			return nil, err
		}

		if err := ensureDirExists(logConfigObj.File.Dir); err != nil {
			return nil, fmt.Errorf("dir unavailable: %w", err)

		} else {
			level, err := parseLevel(logConfigObj.File.Level.String())
			if err != nil {
				return nil, err
			}

			fileWriter := &lumberjack.Logger{
				Filename:   filepath.Join(logConfigObj.File.Dir, target.Name+".log"),
				MaxSize:    maxSizeMB,
				MaxBackups: int(logConfigObj.File.MaxBackups),
				MaxAge:     maxAgeDays,
				Compress:   logConfigObj.File.Compress,
			}

			writers = append(writers, &levelWriterObj{writer: fileWriter, level: level})
			levels = append(levels, level)
			closerArr = append(closerArr, fileWriter)
		}
	}

	if logConfigObj.Victorialogs.Enabled {
		level, err := parseLevel(logConfigObj.Victorialogs.Level.String())
		if err != nil {
			return nil, err
		}

		vlWriterObj, err := newVictorialogsWriter(configObj)
		if err != nil {
			return nil, err
		}

		writers = append(writers, &levelWriterObj{writer: vlWriterObj, level: level})
		levels = append(levels, level)
		closerArr = append(closerArr, vlWriterObj)
		vlSinkObj = vlWriterObj
	}

	if len(writers) == 0 {
		return nil, fmt.Errorf("logger has no enabled sinks")
	}

	effectiveLevel := lowestLevel(levels)
	multiWriter := zerolog.MultiLevelWriter(writers...)

	loggerValue := zerolog.New(multiWriter).
		With().
		Timestamp().
		Str("service", target.Name).
		Str("instance", loggerInstance(configObj)).
		Logger().
		Level(effectiveLevel)
	return &Obj{
		value: loggerValue,
		closerObj: &closerObj{
			closerArr: closerArr,
		},
		vlSink: vlSinkObj,
	}, nil
}
