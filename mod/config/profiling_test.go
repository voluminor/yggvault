package config

import (
	"testing"

	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func TestValidateProfiling(t *testing.T) {
	cases := []struct {
		name    string
		enabled bool
		listen  string
		wantErr bool
	}{
		{"disabled ignores listen", false, "0.0.0.0:6060", false},
		{"loopback ipv4", true, "127.0.0.1:6060", false},
		{"loopback ipv6", true, "[::1]:6060", false},
		{"localhost", true, "localhost:6060", false},
		{"unspecified ipv4", true, "0.0.0.0:6060", true},
		{"empty host", true, ":6060", true},
		{"public ipv4", true, "10.0.0.5:6060", true},
		{"empty listen", true, "", true},
		{"missing port", true, "127.0.0.1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfgObj := stcfg.FullConfig()
			cfgObj.Profiling.Enabled = c.enabled
			cfgObj.Profiling.Listen = c.listen
			err := validateProfiling(cfgObj)
			if c.wantErr && err == nil {
				t.Fatalf("listen=%q enabled=%v: expected error, got nil", c.listen, c.enabled)
			}
			if !c.wantErr && err != nil {
				t.Fatalf("listen=%q enabled=%v: unexpected error: %v", c.listen, c.enabled, err)
			}
		})
	}
}

func TestValidateRegistersProfiling(t *testing.T) {
	cfgObj := newValidConfigObjForTest(t)
	enableWebForTest(cfgObj)
	cfgObj.Profiling.Enabled = true
	cfgObj.Profiling.Listen = "0.0.0.0:6060"

	if err := validate(cfgObj); err == nil {
		t.Fatal("validate() must reject a public profiling.listen (validator not registered?)")
	}
}
