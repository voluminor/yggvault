package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/fatih/color"
)

// // // // // // // // // //

// rxUsageToken highlights long/short flags and angle-bracket placeholders with one matcher.
var rxUsageToken = regexp.MustCompile(`--?[A-Za-z][A-Za-z0-9-]*|<[^>]+>`)

// //

func red(text string) string     { return color.HiRedString(text) }
func green(text string) string   { return color.HiGreenString(text) }
func yellow(text string) string  { return color.HiYellowString(text) }
func blue(text string) string    { return color.HiBlueString(text) }
func magenta(text string) string { return color.HiMagentaString(text) }
func cyan(text string) string    { return color.HiCyanString(text) }

func colorizeUsage(text string) string {
	return rxUsageToken.ReplaceAllStringFunc(text, func(token string) string {
		if token[0] == '<' {
			return blue(token)
		}
		return cyan(token)
	})
}

// //

func renderJSON(writer io.Writer, outputObj *commandOutputObj) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(struct {
		OK bool `json:"ok"`
		*commandOutputObj
	}{
		OK:               true,
		commandOutputObj: outputObj,
	})
}

func renderText(writer io.Writer, outputObj *commandOutputObj) error {
	switch outputObj.Command {
	case cCommandHelp:
		_, err := fmt.Fprintln(writer, buildHelpText(outputObj))
		return err
	case cCommandInfo:
		_, err := fmt.Fprintln(writer, buildInfoText(outputObj))
		return err
	case cCommandPreset:
		_, err := fmt.Fprintln(writer, buildPresetText(outputObj))
		return err
	case cCommandValidate:
		_, err := fmt.Fprintln(writer, buildValidateText(outputObj))
		return err
	}
	return fmt.Errorf("unsupported command output: %s", outputObj.Command)
}

func renderCommandOutput(args argsObj, outputObj *commandOutputObj) error {
	if outputObj == nil {
		return nil
	}
	if args.JsonOutput {
		return renderJSON(os.Stdout, outputObj)
	}
	return renderText(color.Output, outputObj)
}

// //

func buildHelpText(outputObj *commandOutputObj) string {
	var buffer strings.Builder

	projectObj := outputObj.Project
	hashText := projectObj.Hash
	if len(hashText) > 8 {
		hashText = hashText[len(hashText)-8:]
	}

	buffer.WriteString(magenta(projectObj.Name))
	buffer.WriteString(" ")
	buffer.WriteString(green(projectObj.Version))
	buffer.WriteString(" ")
	buffer.WriteString(yellow(hashText))
	buffer.WriteString("\n")
	buffer.WriteString("Release and Go module mirror server")

	if len(outputObj.Usage) > 0 {
		buffer.WriteString("\n\nUsage:")
		for _, line := range outputObj.Usage {
			buffer.WriteString("\n  ")
			buffer.WriteString(magenta(projectObj.Name))
			buffer.WriteString(" ")
			buffer.WriteString(colorizeUsage(line))
		}
	}

	if len(outputObj.Flags) > 0 {
		maxFlagWidth := 0
		for _, flagObj := range outputObj.Flags {
			if len(flagObj.Name) > maxFlagWidth {
				maxFlagWidth = len(flagObj.Name)
			}
		}
		buffer.WriteString("\n\nFlags:")
		for _, flagObj := range outputObj.Flags {
			buffer.WriteString("\n  ")
			buffer.WriteString(colorizeUsage(flagObj.Name))
			buffer.WriteString(strings.Repeat(" ", maxFlagWidth-len(flagObj.Name)+2))
			buffer.WriteString(flagObj.Usage)
		}
	}

	if len(outputObj.Notes) > 0 {
		buffer.WriteString("\n\nNotes:")
		for _, noteText := range outputObj.Notes {
			buffer.WriteString("\n  ")
			buffer.WriteString(noteText)
		}
	}

	return buffer.String()
}

func buildInfoText(outputObj *commandOutputObj) string {
	var buffer strings.Builder

	projectObj := outputObj.Project
	hashText := projectObj.Hash
	if len(hashText) > 8 {
		hashText = hashText[:8]
	}

	buffer.WriteString(magenta(projectObj.Name))
	buffer.WriteString(" ")
	buffer.WriteString(green(projectObj.Version))
	buffer.WriteString(" ")
	buffer.WriteString(yellow(hashText))

	if len(outputObj.Dependencies) == 0 {
		return buffer.String()
	}

	keys := make([]string, 0, len(outputObj.Dependencies))
	for key := range outputObj.Dependencies {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	buffer.WriteString("\n")
	buffer.WriteString("Dependencies used:")
	for _, key := range keys {
		buffer.WriteString("\n\t")
		buffer.WriteString(key)
		buffer.WriteString(" ")
		buffer.WriteString(blue(outputObj.Dependencies[key]))
	}
	return buffer.String()
}

func buildPresetText(outputObj *commandOutputObj) string {
	var buffer strings.Builder
	presetObj := outputObj.Preset
	buffer.WriteString(green("Preset created successfully."))
	buffer.WriteString("\n  preset:\t")
	buffer.WriteString(cyan(presetObj.Name))
	buffer.WriteString("\n  format:\t")
	buffer.WriteString(cyan(presetObj.Format))
	buffer.WriteString("\n  dir:\t")
	buffer.WriteString(presetObj.DirPath)
	buffer.WriteString("\n  file:\t")
	buffer.WriteString(green(presetObj.FilePath))
	if presetObj.Force {
		buffer.WriteString("\n  mode:\t")
		buffer.WriteString(yellow(cFlagForce))
	}
	return buffer.String()
}

func buildValidateText(outputObj *commandOutputObj) string {
	var buffer strings.Builder
	validationObj := outputObj.Validation
	buffer.WriteString(green("Config is valid."))
	buffer.WriteString("\n  path:\t")
	buffer.WriteString(green(validationObj.Path))
	return buffer.String()
}
