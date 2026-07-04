//go:build !verify

package main

import "fmt"

func main() {
	fmt.Println("build with -tags verify and GOPROXY=<node> to import the vault-mirrored module")
}
