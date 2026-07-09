package yggvault

import "errors"

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

// Parse extracts a valid yggvault block from NodeInfo.
func Parse(nodeInfo map[string]any) (*Obj, error) {
	if !Match(nodeInfo) {
		return nil, errors.New("yggvault sigil is missing or invalid")
	}
	obj := &Obj{}
	obj.ParseParams(nodeInfo)
	if err := validateFields(obj.version, obj.hash, obj.date); err != nil {
		return nil, err
	}
	return obj, nil
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
		valueText, ok := block[key].(string)
		if !ok || len(valueText) > cMaxValueBytes {
			return false
		}
	}
	return block[cKeyVersion] != ""
}
