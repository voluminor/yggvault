package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/voluminor/yggvault/mod/cli"
	"github.com/voluminor/yggvault/mod/maintenance"
)

// // // // // // // // // //

func main() {
	bootObj, err := cli.New()
	if err != nil {
		fail(err)
	}
	if bootObj == nil {
		return
	}

	if bootObj.Keygen.Requested() {
		if err = runMakeYggKey(bootObj.Keygen); err != nil {
			fail(err)
		}
		return
	}

	if bootObj.Maintenance.Requested() {
		if err = runMaintenance(bootObj); err != nil {
			fail(err)
		}
		return
	}

	if err = runRuntime(bootObj); err != nil {
		fail(err)
	}
}

// //

func fail(err error) {
	var reportedObj maintenance.ReportedErrObj
	if errors.As(err, &reportedObj) {
		os.Exit(1)
	}
	if cli.RenderError(err) {
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "Error:", err)
	os.Exit(1)
}

func runMaintenance(bootObj *cli.Obj) error {
	ctx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	requestObj := maintenance.RequestObj{
		Inspect:      bootObj.Maintenance.Inspect,
		Prune:        bootObj.Maintenance.Prune,
		Vacuum:       bootObj.Maintenance.Vacuum,
		RebuildCache: bootObj.Maintenance.RebuildCache,
		Force:        bootObj.Maintenance.Force,
		JsonOutput:   bootObj.Maintenance.JsonOutput,
	}
	return maintenance.Run(ctx, bootObj.Config, requestObj, *bootObj.Logger.Zero())
}
