package cli

import (
	"testing"
)

// // // // // // // // // //

func TestParseArgsInterspersedFlags(t *testing.T) {
	testList := []struct {
		name string
		args []string
	}{
		{name: "flags before path", args: []string{"--inspect", "--json", "cfg.yml"}},
		{name: "path before json flag", args: []string{"--inspect", "cfg.yml", "--json"}},
		{name: "path between flags", args: []string{"--json", "cfg.yml", "--inspect"}},
		{name: "json eq-form after path", args: []string{"--inspect", "cfg.yml", "--json=true"}},
	}
	for _, tc := range testList {
		t.Run(tc.name, func(t *testing.T) {
			obj, err := parseArgs(tc.args)
			if err != nil {
				t.Fatalf("parseArgs(%v) error: %v", tc.args, err)
			}
			if obj.Command != CommandInspect {
				t.Fatalf("Command=%q, want %q", obj.Command, CommandInspect)
			}
			if obj.ConfigPath != "cfg.yml" {
				t.Fatalf("ConfigPath=%q, want cfg.yml", obj.ConfigPath)
			}
			if !obj.JsonOutput {
				t.Fatalf("JsonOutput=false, want true")
			}
		})
	}
}

func TestParseArgsRejectsTwoPositionals(t *testing.T) {
	if _, err := parseArgs([]string{"--inspect", "a.yml", "b.yml"}); err == nil {
		t.Fatalf("parseArgs accepted two positional paths, want error")
	}
}
