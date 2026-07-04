package state

import (
	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func (obj *Obj) setContentChecksumLocked(contentObj core.HashObj) bool {
	if obj.checksums.Content == contentObj {
		return false
	}
	obj.checksums.Content = contentObj
	return true
}

// //

// SetContentChecksum updates the content checksum: the cross-key page-freshness fingerprint used in ETags.
func (obj *Obj) SetContentChecksum(contentObj core.HashObj) {
	if err := ensureObj(obj); err != nil {
		return
	}

	obj.lockObj.Lock()
	defer obj.lockObj.Unlock()

	obj.publishMetaChangedLocked(obj.setContentChecksumLocked(contentObj))
}
