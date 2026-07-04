//go:build unix

package main

import "syscall"

// // // // // // // // // //

// cpuSeconds — total process CPU time (user+sys) via getrusage.
func cpuSeconds() float64 {
	var ru syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	tv := func(t syscall.Timeval) float64 { return float64(t.Sec) + float64(t.Usec)/1e6 }
	return tv(ru.Utime) + tv(ru.Stime)
}
