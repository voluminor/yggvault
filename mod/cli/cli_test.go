package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// // // // // // // // // //

func TestParseArgs(t *testing.T) {
	testList := []struct {
		name     string
		args     []string
		expected argsObj
	}{
		{
			name: "runtime positional",
			args: []string{"config.yml"},
			expected: argsObj{
				ConfigPath: "config.yml",
			},
		},
		{
			name: "runtime flag",
			args: []string{"--config", "config.yml"},
			expected: argsObj{
				ConfigPath: "config.yml",
			},
		},
		{
			name: "help json",
			args: []string{"--help", "--json"},
			expected: argsObj{
				Command:    cCommandHelp,
				ShowHelp:   true,
				JsonOutput: true,
			},
		},
		{
			name: "info short",
			args: []string{"-i"},
			expected: argsObj{
				Command:  cCommandInfo,
				ShowInfo: true,
			},
		},
		{
			name: "preset",
			args: []string{"--make-preset", presetMedium, "--out", "./tmp", "--force"},
			expected: argsObj{
				Command:    cCommandPreset,
				MakePreset: presetMedium,
				OutPath:    "./tmp",
				Force:      true,
			},
		},
		{
			name: "preset json format",
			args: []string{"--make-preset", presetFull, "--format", "JSON"},
			expected: argsObj{
				Command:    cCommandPreset,
				MakePreset: presetFull,
				Format:     presetFormatJSON,
			},
		},
		{
			name: "validate",
			args: []string{"--validate-config", "config.yml", "--json"},
			expected: argsObj{
				Command:            cCommandValidate,
				ValidateConfigPath: "config.yml",
				JsonOutput:         true,
			},
		},
	}

	for _, testObj := range testList {
		t.Run(testObj.name, func(t *testing.T) {
			gotObj, err := parseArgs(testObj.args)
			if err != nil {
				t.Fatalf("parseArgs returned error: %v", err)
			}
			if gotObj != testObj.expected {
				t.Fatalf("unexpected args: got %#v want %#v", gotObj, testObj.expected)
			}
		})
	}
}

func TestParseArgsErrors(t *testing.T) {
	testList := []struct {
		name            string
		args            []string
		expectedCommand string
		expectedText    string
	}{
		{
			name:            "preset required for out",
			args:            []string{"--out", "./tmp"},
			expectedCommand: cCommandPreset,
			expectedText:    "--out can only be used with --make-preset",
		},
		{
			name:            "validate cannot mix runtime",
			args:            []string{"--validate-config", "a.yml", "--config", "b.yml"},
			expectedCommand: cCommandValidate,
			expectedText:    "--validate-config cannot be combined with runtime config path",
		},
		{
			name:            "unsupported preset",
			args:            []string{"--make-preset", "broken"},
			expectedCommand: cCommandPreset,
			expectedText:    "unsupported preset",
		},
		{
			name:            "unsupported preset format",
			args:            []string{"--make-preset", presetFull, "--format", "toml"},
			expectedCommand: cCommandPreset,
			expectedText:    "unsupported preset format",
		},
		{
			name:            "format requires preset",
			args:            []string{"--format", "json"},
			expectedCommand: cCommandPreset,
			expectedText:    "--format can only be used with --make-preset",
		},
	}

	for _, testObj := range testList {
		t.Run(testObj.name, func(t *testing.T) {
			_, err := parseArgs(testObj.args)
			if err == nil {
				t.Fatal("parseArgs returned nil error")
			}

			var cliErrObj *cmdErrorObj
			if !errors.As(err, &cliErrObj) {
				t.Fatalf("unexpected error type: %T", err)
			}
			if cliErrObj.command != testObj.expectedCommand {
				t.Fatalf("unexpected command: got %q want %q", cliErrObj.command, testObj.expectedCommand)
			}
			if !strings.Contains(cliErrObj.Error(), testObj.expectedText) {
				t.Fatalf("unexpected error text: %q", cliErrObj.Error())
			}
		})
	}
}

func TestRenderJSON(t *testing.T) {
	bufferObj := bytes.NewBuffer(nil)

	err := renderJSON(bufferObj, &commandOutputObj{
		Command: cCommandInfo,
		Project: newProjectInfo(),
		Notes:   []string{"note"},
	})
	if err != nil {
		t.Fatalf("renderJSON returned error: %v", err)
	}

	var payload map[string]any
	if err = json.Unmarshal(bufferObj.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if payload["ok"] != true {
		t.Fatalf("unexpected ok value: %#v", payload["ok"])
	}
	if payload["command"] != cCommandInfo {
		t.Fatalf("unexpected command: %#v", payload["command"])
	}
	if _, ok := payload["project"].(map[string]any); !ok {
		t.Fatalf("project field missing or invalid: %#v", payload["project"])
	}
	if _, ok := payload["data"]; ok {
		t.Fatalf("unexpected nested data field: %#v", payload["data"])
	}
}

func TestGeneratePreset(t *testing.T) {
	dirPath := t.TempDir()

	infoObj, err := generatePreset(argsObj{
		Command:    cCommandPreset,
		MakePreset: presetMinimal,
		OutPath:    dirPath,
	})
	if err != nil {
		t.Fatalf("generatePreset returned error: %v", err)
	}
	if infoObj.FilePath != filepath.Join(dirPath, cPresetFileName) {
		t.Fatalf("unexpected file path: %q", infoObj.FilePath)
	}

	data, err := os.ReadFile(infoObj.FilePath)
	if err != nil {
		t.Fatalf("failed to read preset file: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("preset file is empty")
	}
}

func TestGeneratePresetJSON(t *testing.T) {
	dirPath := t.TempDir()

	infoObj, err := generatePreset(argsObj{
		Command:    cCommandPreset,
		MakePreset: presetFull,
		Format:     presetFormatJSON,
		OutPath:    dirPath,
	})
	if err != nil {
		t.Fatalf("generatePreset(json) returned error: %v", err)
	}
	if filepath.Base(infoObj.FilePath) != "config.json" {
		t.Fatalf("unexpected file path: %q", infoObj.FilePath)
	}
	if infoObj.Format != presetFormatJSON {
		t.Fatalf("unexpected format: %q", infoObj.Format)
	}

	data, err := os.ReadFile(infoObj.FilePath)
	if err != nil {
		t.Fatalf("failed to read preset file: %v", err)
	}
	var payload map[string]any
	if err = json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("preset json is invalid: %v", err)
	}
}

func TestGeneratePresetExistingWithoutForce(t *testing.T) {
	dirPath := t.TempDir()
	filePath := filepath.Join(dirPath, cPresetFileName)
	if err := os.WriteFile(filePath, []byte("existing"), 0o644); err != nil {
		t.Fatalf("failed to prepare preset file: %v", err)
	}

	_, err := generatePreset(argsObj{
		Command:    cCommandPreset,
		MakePreset: presetMinimal,
		OutPath:    dirPath,
	})
	if err == nil {
		t.Fatal("generatePreset returned nil error")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("unexpected error: %v", err)
	}
}
