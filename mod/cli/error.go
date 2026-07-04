package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/fatih/color"
)

// // // // // // // // // //

type cmdErrorObj struct {
	command    string
	jsonOutput bool
	err        error
}

// //

// Error returns the nested error text.
func (obj *cmdErrorObj) Error() string {
	return obj.err.Error()
}

// Unwrap exposes the wrapped error for errors.Is/As.
func (obj *cmdErrorObj) Unwrap() error {
	return obj.err
}

// //

func cmdError(args argsObj, command string, err error) error {
	if err == nil {
		return nil
	}
	return &cmdErrorObj{
		command:    command,
		jsonOutput: args.JsonOutput,
		err:        err,
	}
}

// RenderError renders a CLI error: JSON envelope with --json, colored stderr otherwise.
func RenderError(err error) bool {
	var cliErrObj *cmdErrorObj
	if !errors.As(err, &cliErrObj) {
		return false
	}

	if cliErrObj.jsonOutput {
		encoder := json.NewEncoder(os.Stderr)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(struct {
			OK      bool   `json:"ok"`
			Command string `json:"command,omitempty"`
			Error   string `json:"error"`
		}{
			OK:      false,
			Command: cliErrObj.command,
			Error:   cliErrObj.Error(),
		})
		return true
	}

	_, _ = fmt.Fprintln(color.Error, red("Error:")+" "+cliErrObj.Error())
	return true
}
