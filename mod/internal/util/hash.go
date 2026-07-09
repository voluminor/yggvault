package util

// // // // // // // // // //

// FNV32a returns the 32-bit FNV-1a hash of s, used for cheap map sharding.
func FNV32a(s string) uint32 {
	hashValue := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		hashValue ^= uint32(s[i])
		hashValue *= 16777619
	}
	return hashValue
}
