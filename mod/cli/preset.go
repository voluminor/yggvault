package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/voluminor/yggvault/mod/internal/osfs"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	presetMinimal = "minimal"
	presetMedium  = "medium"
	presetFull    = "full"
)

const (
	presetFormatYAML  = "yaml"
	presetFormatJSON  = "json"
	presetFormatHJSON = "hjson"
)

// cPresetFileName is the default YAML preset filename; json/hjson are handled by presetFileName.
const cPresetFileName = "config.yml"

// //

func resolveFormat(format string) string {
	if format == "" {
		return presetFormatYAML
	}
	return format
}

func presetFileName(format string) string {
	switch resolveFormat(format) {
	case presetFormatJSON:
		return "config.json"
	case presetFormatHJSON:
		return "config.hjson"
	default:
		return cPresetFileName
	}
}

func presetDirPath(rawPath string) string {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return "."
	}
	return trimmed
}

func presetText(name, format string) string {
	full, preset := stcfg.FullYAML, stcfg.PresetYAML
	switch resolveFormat(format) {
	case presetFormatJSON:
		full, preset = stcfg.FullJSON, stcfg.PresetJSON
	case presetFormatHJSON:
		full, preset = stcfg.FullHJSON, stcfg.PresetHJSON
	}
	switch name {
	case presetMinimal, presetMedium:
		if data, ok := preset(name); ok {
			return string(data)
		}
	case presetFull:
		return string(full())
	}
	return ""
}

func ensurePresetDir(dirPath string, force bool) error {
	info, err := os.Stat(dirPath)
	if err == nil {
		if info.IsDir() {
			return nil
		}
		return fmt.Errorf("output path is not a directory: %s", dirPath)
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("stat output directory %s: %w", dirPath, err)
	}
	if !force {
		return fmt.Errorf("output directory does not exist: %s", dirPath)
	}
	if err = os.MkdirAll(dirPath, 0o755); err != nil {
		return fmt.Errorf("create output directory %s: %w", dirPath, err)
	}
	return nil
}

func ensurePresetFileWritable(filePath string, force bool) error {
	if force {
		return nil
	}
	_, err := os.Stat(filePath)
	if err == nil {
		return fmt.Errorf("output file already exists: %s", filePath)
	}
	if os.IsNotExist(err) {
		return nil
	}
	return fmt.Errorf("stat output file %s: %w", filePath, err)
}

// //

func generatePreset(args argsObj) (*presetInfoObj, error) {
	text := presetText(args.MakePreset, args.Format)
	if text == "" {
		return nil, fmt.Errorf("preset content unavailable: %s", args.MakePreset)
	}

	dirPath, err := filepath.Abs(presetDirPath(args.OutPath))
	if err != nil {
		return nil, fmt.Errorf("resolve output directory: %w", err)
	}
	filePath := filepath.Join(dirPath, presetFileName(args.Format))

	if err = ensurePresetDir(dirPath, args.Force); err != nil {
		return nil, err
	}
	if err = ensurePresetFileWritable(filePath, args.Force); err != nil {
		return nil, err
	}

	fileObj, err := osfs.OpenNoFollowWrite(filePath, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open preset file %s: %w", filePath, err)
	}
	if _, err = fileObj.Write([]byte(text)); err != nil {
		_ = fileObj.Close()
		return nil, fmt.Errorf("write preset file %s: %w", filePath, err)
	}
	if err = fileObj.Close(); err != nil {
		return nil, fmt.Errorf("close preset file %s: %w", filePath, err)
	}

	return &presetInfoObj{
		Name:     args.MakePreset,
		Format:   resolveFormat(args.Format),
		DirPath:  dirPath,
		FilePath: filePath,
		Force:    args.Force,
	}, nil
}

func handlePreset(args argsObj) error {
	presetObj, err := generatePreset(args)
	if err != nil {
		return cmdError(args, cCommandPreset, err)
	}
	return cmdError(args, cCommandPreset, renderCommandOutput(args, &commandOutputObj{
		Command: cCommandPreset,
		Project: newProjectInfo(),
		Preset:  presetObj,
	}))
}
