//go:build !unix

package main

// // // // // // // // // //

// cpuSeconds — getrusage is unavailable outside unix; the bench runs only in linux-docker, this is a stub.
func cpuSeconds() float64 {
	return 0
}
