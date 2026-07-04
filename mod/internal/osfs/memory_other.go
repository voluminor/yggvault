//go:build !linux

package osfs

// // // // // // // // // //

// SystemMemoryBytes is unknown on unsupported platforms; 0 disables RAM checks.
func SystemMemoryBytes() uint64 {
	return 0
}
