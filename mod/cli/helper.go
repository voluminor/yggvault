package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/voluminor/yggvault/target"
)

// // // // // // // // // //

func dash(name string) string {
	return "--" + name
}

// //

func parseFlags(args []string) (argsObj, []string, error) {
	fs := flag.NewFlagSet(target.Name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	cfg := fs.String(cFlagConfig, "", "path to config file")
	validateCfg := fs.String(cCommandValidate, "", "validate config file and exit")
	preset := fs.String(cCommandPreset, "", "generate preset: minimal|medium|full")
	makeYggKey := fs.Bool(cCommandMakeYggKey, false, "generate a Yggdrasil node private key in the current directory")
	out := fs.String(cFlagOut, "", "output directory for "+dash(cCommandPreset))
	format := fs.String(cFlagFormat, "", "preset output format: yaml|json|hjson (default yaml)")
	force := fs.Bool(cFlagForce, false, "allow directory creation and overwrite existing config.yml")
	helpShort := fs.Bool(cFlagHelpShort, false, "show help")
	helpLong := fs.Bool(cCommandHelp, false, "show help")
	infoShort := fs.Bool(cFlagInfoShort, false, "show project info")
	infoLong := fs.Bool(cCommandInfo, false, "show project info")
	jsonOutput := fs.Bool(cFlagJSON, false, "render command output as JSON")
	inspect := fs.Bool(CommandInspect, false, "inspect storage (read-only): keys, versions, sizes, orphan estimate")
	prune := fs.Bool(CommandPrune, false, "delete keys absent from release_mirrors + orphan GC (dry-run unless "+dash(cFlagForce)+")")
	vacuum := fs.Bool(CommandVacuum, false, "reclaim disk: SQLite VACUUM + Pebble compaction")
	rebuildCache := fs.Bool(CommandRebuildCache, false, "re-materialize artifacts and refresh body hashes under the current toolchain")

	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return argsObj{}, nil, fmt.Errorf("flag parse: %w", err)
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}

	return argsObj{
		ConfigPath:         strings.TrimSpace(*cfg),
		ValidateConfigPath: strings.TrimSpace(*validateCfg),
		MakePreset:         strings.TrimSpace(*preset),
		MakeYggKey:         *makeYggKey,
		OutPath:            strings.TrimSpace(*out),
		Format:             strings.ToLower(strings.TrimSpace(*format)),
		Force:              *force,
		ShowHelp:           *helpShort || *helpLong,
		ShowInfo:           *infoShort || *infoLong,
		JsonOutput:         *jsonOutput,
		Inspect:            *inspect,
		Prune:              *prune,
		Vacuum:             *vacuum,
		RebuildCache:       *rebuildCache,
	}, positional, nil
}

func resolveCommand(obj argsObj) string {
	switch {
	case obj.ShowHelp:
		return cCommandHelp
	case obj.ShowInfo:
		return cCommandInfo
	case obj.ValidateConfigPath != "":
		return cCommandValidate
	case obj.MakeYggKey:
		return cCommandMakeYggKey
	case obj.Inspect:
		return CommandInspect
	case obj.Prune:
		return CommandPrune
	case obj.Vacuum:
		return CommandVacuum
	case obj.RebuildCache:
		return CommandRebuildCache
	case obj.MakePreset != "" || obj.OutPath != "" || obj.Format != "" || obj.Force:
		return cCommandPreset
	default:
		return ""
	}
}

func finalizeArgs(obj argsObj, positional []string) argsObj {
	if obj.ConfigPath == "" && len(positional) == 1 {
		obj.ConfigPath = strings.TrimSpace(positional[0])
	}
	obj.Command = resolveCommand(obj)
	return obj
}

// //

func validatePresetOnlyFlags(obj argsObj) error {
	if obj.OutPath != "" {
		return errors.New(dash(cFlagOut) + " can only be used with " + dash(cCommandPreset))
	}
	if obj.Force {
		return errors.New(dash(cFlagForce) + " can only be used with " + dash(cCommandPreset))
	}
	if obj.Format != "" {
		return errors.New(dash(cFlagFormat) + " can only be used with " + dash(cCommandPreset))
	}
	return nil
}

func validatePresetFormat(format string) error {
	switch format {
	case "", presetFormatYAML, presetFormatJSON, presetFormatHJSON:
		return nil
	default:
		return fmt.Errorf("unsupported preset format: %s (allowed: %s|%s|%s)",
			format, presetFormatYAML, presetFormatJSON, presetFormatHJSON)
	}
}

func validateValidateArgs(obj argsObj) error {
	if obj.ConfigPath != "" {
		return errors.New(dash(cCommandValidate) + " cannot be combined with runtime config path")
	}
	if obj.MakePreset != "" {
		return errors.New(dash(cCommandValidate) + " cannot be combined with " + dash(cCommandPreset))
	}
	return validatePresetOnlyFlags(obj)
}

func validatePresetArgs(obj argsObj) error {
	switch obj.MakePreset {
	case presetMinimal, presetMedium, presetFull:
	case "":
		return validatePresetOnlyFlags(obj)
	default:
		return fmt.Errorf("unsupported preset: %s (allowed: %s|%s|%s)",
			obj.MakePreset, presetMinimal, presetMedium, presetFull)
	}
	if obj.ConfigPath != "" {
		return errors.New(dash(cCommandPreset) + " cannot be combined with config path")
	}
	return validatePresetFormat(obj.Format)
}

func validateMakeYggKeyArgs(obj argsObj) error {
	if obj.ConfigPath != "" {
		return errors.New(dash(cCommandMakeYggKey) + " does not take a config path")
	}
	if obj.ValidateConfigPath != "" || obj.MakePreset != "" {
		return errors.New(dash(cCommandMakeYggKey) + " cannot be combined with " + dash(cCommandValidate) + " or " + dash(cCommandPreset))
	}
	if obj.OutPath != "" || obj.Format != "" {
		return errors.New(dash(cFlagOut) + "/" + dash(cFlagFormat) + " are only valid with " + dash(cCommandPreset))
	}
	if obj.Inspect || obj.Prune || obj.Vacuum || obj.RebuildCache {
		return errors.New(dash(cCommandMakeYggKey) + " cannot be combined with maintenance commands")
	}
	return nil
}

func validateMaintenanceArgs(obj argsObj) error {
	if boolCount(obj.Inspect, obj.Prune, obj.Vacuum, obj.RebuildCache) > 1 {
		return errors.New("only one maintenance command (" + dash(CommandInspect) + "/" + dash(CommandPrune) + "/" + dash(CommandVacuum) + "/" + dash(CommandRebuildCache) + ") may be used at a time")
	}
	if obj.ValidateConfigPath != "" {
		return errors.New("maintenance commands cannot be combined with " + dash(cCommandValidate))
	}
	if obj.MakePreset != "" || obj.OutPath != "" {
		return errors.New("maintenance commands cannot be combined with " + dash(cCommandPreset) + " or " + dash(cFlagOut))
	}
	if obj.Force && !obj.Prune {
		return errors.New(dash(cFlagForce) + " is only valid with " + dash(CommandPrune))
	}
	if obj.ConfigPath == "" {
		return errors.New("config path is required for maintenance commands")
	}
	return nil
}

func boolCount(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

func validateArgs(obj argsObj, positional []string) error {
	if len(positional) > 1 {
		return errors.New("only one positional config path is allowed")
	}

	switch obj.Command {
	case cCommandHelp, cCommandInfo:
		return nil
	case cCommandValidate:
		return validateValidateArgs(obj)
	case cCommandPreset:
		return validatePresetArgs(obj)
	case cCommandMakeYggKey:
		return validateMakeYggKeyArgs(obj)
	case CommandInspect, CommandPrune, CommandVacuum, CommandRebuildCache:
		return validateMaintenanceArgs(obj)
	}

	if err := validatePresetOnlyFlags(obj); err != nil {
		return err
	}
	if obj.ConfigPath == "" {
		return errors.New("config path is required")
	}
	return nil
}

// //

func parseArgs(args []string) (argsObj, error) {
	obj, positional, err := parseFlags(args)
	if err != nil {
		return obj, err
	}
	obj = finalizeArgs(obj, positional)
	if err = validateArgs(obj, positional); err != nil {
		return obj, cmdError(obj, obj.Command, err)
	}
	return obj, nil
}
