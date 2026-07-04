package cli

import (
	"github.com/voluminor/yggvault/mod/logger"
	"github.com/voluminor/yggvault/target"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	cCommandHelp       = "help"
	cCommandInfo       = "info"
	cCommandPreset     = "make-preset"
	cCommandMakeYggKey = "make-ygg-key"
	cCommandValidate   = "validate-config"

	// Maintenance command names are public and shared by cli flags/validation/help and root main JSON rendering.
	CommandInspect      = "inspect"
	CommandPrune        = "prune"
	CommandVacuum       = "vacuum"
	CommandRebuildCache = "rebuild-cache"
)

// Non-command flag names; values are shared by flag.FlagSet, help text, and errors.
const (
	cFlagConfig    = "config"
	cFlagOut       = "out"
	cFlagFormat    = "format"
	cFlagForce     = "force"
	cFlagJSON      = "json"
	cFlagHelpShort = "h"
	cFlagInfoShort = "i"
)

// //

// Obj is the CLI bootstrap result for runtime mode: validated config, ready logger, and requested maintenance flags.
// New returns it; root main performs module orchestration.
type Obj struct {
	Config      *stcfg.ConfigObj
	Logger      *logger.Obj
	Maintenance MaintenanceObj
	Keygen      KeygenObj
}

// MaintenanceObj holds the requested CLI maintenance command. cli only recognizes and validates it; main opens
// storage, executes work, and renders output.
type MaintenanceObj struct {
	Inspect      bool
	Prune        bool
	Vacuum       bool
	RebuildCache bool
	Force        bool // apply --prune; without it prune is dry-run
	JsonOutput   bool // machine-readable output
}

// Requested reports whether any maintenance command was requested.
func (obj MaintenanceObj) Requested() bool {
	return obj.Inspect || obj.Prune || obj.Vacuum || obj.RebuildCache
}

// KeygenObj is the config-less ygg key generation command. cli only recognizes and validates it; main generates,
// writes, and renders the result.
type KeygenObj struct {
	MakeYggKey bool
	Force      bool // overwrite existing key file
	JsonOutput bool // machine-readable output
}

// Requested reports whether key generation was requested.
func (obj KeygenObj) Requested() bool {
	return obj.MakeYggKey
}

type argsObj struct {
	Command            string
	ConfigPath         string
	ValidateConfigPath string
	MakePreset         string
	MakeYggKey         bool
	OutPath            string
	Format             string
	Force              bool
	ShowHelp           bool
	ShowInfo           bool
	JsonOutput         bool
	Inspect            bool
	Prune              bool
	Vacuum             bool
	RebuildCache       bool
}

type commandOutputObj struct {
	Command      string             `json:"command"`
	Project      *projectInfoObj    `json:"project,omitempty"`
	Usage        []string           `json:"usage,omitempty"`
	Flags        []flagInfoObj      `json:"flags,omitempty"`
	Notes        []string           `json:"notes,omitempty"`
	Dependencies map[string]string  `json:"dependencies,omitempty"`
	Preset       *presetInfoObj     `json:"preset,omitempty"`
	Validation   *validationInfoObj `json:"validation,omitempty"`
}

type projectInfoObj struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Hash    string `json:"hash"`
}

type flagInfoObj struct {
	Name  string `json:"name"`
	Usage string `json:"usage"`
}

type presetInfoObj struct {
	Name     string `json:"name"`
	Format   string `json:"format"`
	DirPath  string `json:"dir_path"`
	FilePath string `json:"file_path"`
	Force    bool   `json:"force"`
}

type validationInfoObj struct {
	Path  string `json:"path"`
	Valid bool   `json:"valid"`
}

// //

func newProjectInfo() *projectInfoObj {
	return &projectInfoObj{
		Name:    target.Name,
		Version: target.Version,
		Hash:    target.Hash,
	}
}
