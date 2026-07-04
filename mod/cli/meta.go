package cli

import (
	"github.com/voluminor/yggvault/target"
)

// // // // // // // // // //

// //

func buildHelpOutput() *commandOutputObj {
	return &commandOutputObj{
		Command: cCommandHelp,
		Project: newProjectInfo(),
		Usage: []string{
			"[" + dash(cFlagConfig) + " <path> | <path>]",
			dash(cCommandValidate) + " <path> [" + dash(cFlagJSON) + "]",
			dash(cCommandPreset) + " <minimal|medium|full> [" + dash(cFlagOut) + " <dir>] [" + dash(cFlagFormat) + " <yaml|json|hjson>] [" + dash(cFlagForce) + "] [" + dash(cFlagJSON) + "]",
			dash(cCommandMakeYggKey) + " [" + dash(cFlagForce) + "] [" + dash(cFlagJSON) + "]",
			dash(CommandInspect) + " <path> [" + dash(cFlagJSON) + "]",
			dash(CommandVacuum) + " <path> [" + dash(cFlagJSON) + "]",
			dash(CommandPrune) + " <path> [" + dash(cFlagForce) + "] [" + dash(cFlagJSON) + "]",
			dash(CommandRebuildCache) + " <path> [" + dash(cFlagJSON) + "]",
			dash(cCommandInfo) + " [" + dash(cFlagJSON) + "]",
			dash(cCommandHelp) + " [" + dash(cFlagJSON) + "]",
		},
		Flags: []flagInfoObj{
			{Name: dash(cFlagConfig) + " <path>", Usage: "load config and start runtime server"},
			{Name: dash(cCommandValidate) + " <path>", Usage: "validate config file and exit without runtime start"},
			{Name: dash(cCommandPreset) + " <minimal|medium|full>", Usage: "generate preset config in the chosen format (default yaml)"},
			{Name: dash(cFlagOut) + " <dir>", Usage: "output directory for " + dash(cCommandPreset)},
			{Name: dash(cFlagFormat) + " <yaml|json|hjson>", Usage: "with " + dash(cCommandPreset) + ": output format (default yaml); file is config.yml/.json/.hjson"},
			{Name: dash(cCommandMakeYggKey), Usage: "generate a Yggdrasil node private key (PKCS#8 PEM) in the current directory"},
			{Name: dash(cFlagForce), Usage: "with " + dash(cCommandPreset) + ": allow dir creation and overwrite; with " + dash(CommandPrune) + ": apply deletions (default dry-run); with " + dash(cCommandMakeYggKey) + ": overwrite the existing key file"},
			{Name: dash(CommandInspect) + " <path>", Usage: "inspect storage read-only: keys, versions, sizes, orphan estimate (server must be stopped)"},
			{Name: dash(CommandVacuum) + " <path>", Usage: "reclaim disk: SQLite VACUUM + Pebble compaction (server must be stopped)"},
			{Name: dash(CommandPrune) + " <path>", Usage: "delete keys absent from release_mirrors + orphan GC; dry-run unless " + dash(cFlagForce) + " (server must be stopped)"},
			{Name: dash(CommandRebuildCache) + " <path>", Usage: "re-materialize artifacts and refresh body hashes under the current toolchain (server must be stopped)"},
			{Name: dash(cCommandInfo) + ", -" + cFlagInfoShort, Usage: "show project info"},
			{Name: dash(cCommandHelp) + ", -" + cFlagHelpShort, Usage: "show help"},
			{Name: dash(cFlagJSON), Usage: "render command output as JSON (help, info, preset, validation, maintenance)"},
		},
		Notes: []string{
			"[" + dash(cFlagConfig) + " <path> | <path>] is the runtime command path and starts the server after successful config and logger initialization",
			dash(cCommandValidate) + " checks the same full startup validation path but exits without entering runtime",
			"maintenance commands (" + dash(CommandInspect) + "/" + dash(CommandPrune) + "/" + dash(CommandVacuum) + "/" + dash(CommandRebuildCache) + ") require exclusive storage access — stop the running server first",
			dash(cFlagJSON) + " affects only command output",
			"runtime with " + dash(cFlagJSON) + " continues normally and emits a warning to logger",
			"command errors also switch to JSON when " + dash(cFlagJSON) + " is enabled",
		},
	}
}

func buildInfoOutput() *commandOutputObj {
	return &commandOutputObj{
		Command:      cCommandInfo,
		Project:      newProjectInfo(),
		Dependencies: target.VersionByModule,
	}
}

// //

func handleMeta(args argsObj) error {
	if args.ShowHelp {
		return cmdError(args, cCommandHelp, renderCommandOutput(args, buildHelpOutput()))
	}
	return cmdError(args, cCommandInfo, renderCommandOutput(args, buildInfoOutput()))
}
