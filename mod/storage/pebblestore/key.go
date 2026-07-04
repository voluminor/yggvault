package pebblestore

import "github.com/voluminor/yggvault/mod/core"

// // // // // // // // // //

const (
	cObjectKindBlob objectKindObj = iota + 1
	cObjectKindTree
)

// //

func objectKey(kindObj objectKindObj, hashObj core.HashObj) keyObj {
	var keyArr keyObj
	keyArr[0], _ = objectTag(kindObj)
	copy(keyArr[1:], hashObj[:])
	return keyArr
}

func blobKey(hashObj core.HashObj) keyObj {
	return objectKey(cObjectKindBlob, hashObj)
}

func treeKey(hashObj core.HashObj) keyObj {
	return objectKey(cObjectKindTree, hashObj)
}

func objectHashFromKey(kindObj objectKindObj, keyArr []byte) (core.HashObj, bool) {
	var emptyObj core.HashObj
	keyTag, ok := objectTag(kindObj)
	if !ok || len(keyArr) != cKeySize || keyArr[0] != keyTag {
		return emptyObj, false
	}
	hashObj, err := core.HashFromBytes(keyArr[1:])
	if err != nil {
		return emptyObj, false
	}
	return hashObj, true
}

func objectTag(kindObj objectKindObj) (byte, bool) {
	switch kindObj {
	case cObjectKindBlob:
		return cBlobTag, true
	case cObjectKindTree:
		return cTreeTag, true
	default:
		return 0, false
	}
}
