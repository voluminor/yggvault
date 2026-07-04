package yggvault

// // // // // // // // // //

// Name returns the sigil name, which is the top-level key.
func Name() string { return sigName }

// Keys returns top-level keys owned by the sigil.
func Keys() []string { return sigKeys }

// //

// ParseParams extracts this sigil block from another NodeInfo.
func ParseParams(nodeInfo map[string]any) map[string]any {
	bufMap := make(map[string]any)
	if data, ok := nodeInfo[sigName]; ok {
		bufMap[sigName] = data
	}
	return bufMap
}

// Match reports whether another NodeInfo contains this sigil with the expected structure.
func Match(nodeInfo map[string]any) bool {
	raw, ok := nodeInfo[sigName]
	if !ok {
		return false
	}
	block, ok := raw.(map[string]any)
	if !ok {
		return false
	}
	for _, key := range []string{cKeyVersion, cKeyHash, cKeyDate} {
		if _, ok := block[key].(string); !ok {
			return false
		}
	}
	return true
}
