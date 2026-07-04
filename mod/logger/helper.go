package logger

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	stcfg "github.com/voluminor/yggvault/target/stconf"

	"github.com/rs/zerolog"
)

// // // // // // // // // //

type levelWriterObj struct {
	writer io.Writer
	level  zerolog.Level
}

// Write writes without level filtering for io.Writer paths that do not support levels.
func (obj *levelWriterObj) Write(data []byte) (int, error) {
	return obj.writer.Write(data)
}

// WriteLevel drops records below the sink threshold, otherwise delegates to the wrapped writer.
func (obj *levelWriterObj) WriteLevel(level zerolog.Level, data []byte) (int, error) {
	if level < obj.level {
		return len(data), nil
	}

	if levelWriter, ok := obj.writer.(zerolog.LevelWriter); ok {
		return levelWriter.WriteLevel(level, data)
	}

	return obj.writer.Write(data)
}

// //

func parseLevel(levelName string) (zerolog.Level, error) {
	level, err := zerolog.ParseLevel(levelName)
	if err != nil {
		return zerolog.Disabled, fmt.Errorf("unsupported log level: %s", levelName)
	}

	return level, nil
}

func lowestLevel(levels []zerolog.Level) zerolog.Level {
	result := levels[0]
	for i := 1; i < len(levels); i++ {
		if levels[i] < result {
			result = levels[i]
		}
	}
	return result
}

func checkFileConfig(config stcfg.LoggingFileObj) error {
	if strings.TrimSpace(config.Dir) == "" {
		return errors.New("file logger dir is empty")
	}
	if uint64(config.MaxSize) < 1 {
		return errors.New("file logger max_size must be >= 1 byte")
	}
	if config.MaxBackups < 1 {
		return errors.New("file logger max_backups must be >= 1")
	}
	if config.MaxAge < 24*time.Hour {
		return errors.New("file logger max_age must be >= 24h")
	}
	return nil
}

func ensureDirExists(pathToDir string) error {
	absPath, err := filepath.Abs(pathToDir)
	if err != nil {
		return err
	}

	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return err
		}
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("path is not directory: %s", absPath)
	}
	return nil
}

func sizeBytesToMegabytes(sizeObj stcfg.SizeObj) (int, error) {
	sizeBytes := uint64(sizeObj)
	if sizeBytes == 0 {
		return 0, errors.New("file logger max_size must be >= 1 byte")
	}

	sizeMegabytes := (sizeBytes + cMegabyte - 1) / cMegabyte
	if sizeMegabytes > math.MaxInt {
		return 0, fmt.Errorf("file logger max_size is too large: %d bytes", sizeBytes)
	}

	return int(sizeMegabytes), nil
}

func durationToDays(durationObj time.Duration) (int, error) {
	duration := durationObj
	if duration <= 0 {
		return 0, errors.New("file logger max_age must be > 0")
	}

	days := int((duration + 24*time.Hour - 1) / (24 * time.Hour))
	if days < 1 {
		return 0, errors.New("file logger max_age must be >= 24h")
	}

	return days, nil
}
