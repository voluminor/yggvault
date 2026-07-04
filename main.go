package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/voluminor/yggvault/mod/cli"
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
	if errors.Is(err, errAlreadyReported) {
		os.Exit(1)
	}
	if cli.RenderError(err) {
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "Error:", err)
	os.Exit(1)
}
