package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/voluminor/yggvault/mod/config"
	"github.com/voluminor/yggvault/mod/logger"
)

// // // // // // // // // //

func handleValidate(args argsObj) error {
	pathToFile, err := filepath.Abs(args.ValidateConfigPath)
	if err != nil {
		return cmdError(args, cCommandValidate, fmt.Errorf("resolve config path %s: %w", args.ValidateConfigPath, err))
	}
	if _, err = config.New(pathToFile); err != nil {
		return cmdError(args, cCommandValidate, fmt.Errorf("validate %s: %w", pathToFile, err))
	}
	return cmdError(args, cCommandValidate, renderCommandOutput(args, &commandOutputObj{
		Command: cCommandValidate,
		Project: newProjectInfo(),
		Validation: &validationInfoObj{
			Path:  pathToFile,
			Valid: true,
		},
	}))
}

func dispatchCommand(args argsObj) (bool, error) {
	switch args.Command {
	case cCommandHelp, cCommandInfo:
		return true, handleMeta(args)
	case cCommandPreset:
		return true, handlePreset(args)
	case cCommandValidate:
		return true, handleValidate(args)
	}
	return false, nil
}

// //

func maintenanceFromArgs(args argsObj) MaintenanceObj {
	return MaintenanceObj{
		Inspect:      args.Inspect,
		Prune:        args.Prune,
		Vacuum:       args.Vacuum,
		RebuildCache: args.RebuildCache,
		Force:        args.Force,
		JsonOutput:   args.JsonOutput,
	}
}

func keygenFromArgs(args argsObj) KeygenObj {
	return KeygenObj{
		MakeYggKey: args.MakeYggKey,
		Force:      args.Force,
		JsonOutput: args.JsonOutput,
	}
}

func newFromArgs(args argsObj, err error) (*Obj, error) {
	if err != nil {
		return nil, err
	}

	if handled, cmdErr := dispatchCommand(args); handled {
		return nil, cmdErr
	}

	if args.Command == cCommandMakeYggKey {
		return &Obj{Keygen: keygenFromArgs(args)}, nil
	}

	configObj, err := config.New(args.ConfigPath)
	if err != nil {
		return nil, err
	}

	loggerObj, err := logger.New(configObj)
	if err != nil {
		return nil, err
	}

	maintenanceObj := maintenanceFromArgs(args)
	if args.JsonOutput && !maintenanceObj.Requested() {
		loggerObj.Zero().Warn().Msg("--json is intended for command output only and has no effect in runtime mode")
	}

	return &Obj{
		Config:      configObj,
		Logger:      loggerObj,
		Maintenance: maintenanceObj,
	}, nil
}

// New is the full CLI bootstrap point: it parses argv, handles help/info/preset/validate, and returns validated
// config plus initialized logger in runtime mode.
func New() (*Obj, error) {
	args, err := parseArgs(os.Args[1:])
	return newFromArgs(args, err)
}
