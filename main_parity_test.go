package main

import (
	"testing"

	"github.com/voluminor/yggvault/mod/cli"
	"github.com/voluminor/yggvault/mod/maintenance"
)

// // // // // // // // // //

func TestMaintenanceCommandNameParity(t *testing.T) {
	cases := []struct {
		name string
		req  maintenance.RequestObj
		want string
	}{
		{name: "inspect", req: maintenance.RequestObj{Inspect: true}, want: cli.CommandInspect},
		{name: "prune", req: maintenance.RequestObj{Prune: true}, want: cli.CommandPrune},
		{name: "vacuum", req: maintenance.RequestObj{Vacuum: true}, want: cli.CommandVacuum},
		{name: "rebuild-cache", req: maintenance.RequestObj{RebuildCache: true}, want: cli.CommandRebuildCache},
	}
	for _, caseObj := range cases {
		if got := caseObj.req.CommandName(); got != caseObj.want {
			t.Fatalf("%s command name=%q want %q", caseObj.name, got, caseObj.want)
		}
	}
}
