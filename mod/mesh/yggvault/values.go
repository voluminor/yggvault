package yggvault

// // // // // // // // // //

const (
	// sigName is the only top-level sigil key in NodeInfo.
	sigName = "yggvault"

	cKeyVersion = "version"
	cKeyHash    = "hash"
	cKeyDate    = "date"

	cMaxValueBytes = 128
)

var sigKeys = []string{sigName}
