//go:build verify

package main

import (
	"fmt"

	"vault.test/errors"
)

func main() {
	err := errors.Wrap(errors.New("base failure"), "context")
	fmt.Println("import OK via vault:", err)
}
