package state

import (
	"errors"
	"fmt"
	"strings"

	"github.com/voluminor/yggvault/target/stcode"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func newKeyMutObj(key string, sourceURL string) *keyMutObj {
	return &keyMutObj{
		key:          key,
		sourceURL:    sourceURL,
		availability: stcode.AvailabilityStatusUnknown,
	}
}

// //

// New builds a registry from validated config.
// It seeds release_mirrors, initializes the instance checksum, and publishes the first snapshot.
func New(configObj *stcfg.ConfigObj) (*Obj, error) {
	if configObj == nil {
		return nil, errors.New("state config is nil")
	}
	if len(configObj.ReleaseMirrors) == 0 {
		return nil, errors.New("release_mirrors must contain at least one entry")
	}
	if len(configObj.ReleaseMirrors) > cMaxKeys {
		return nil, errors.New("release_mirrors exceeds maximum key count")
	}

	permanentValue := configObj.UpstreamAvailability.PermanentAfterCycles
	if permanentValue < 1 {
		return nil, errors.New("upstream_availability.permanent_after_cycles must be >= 1")
	}
	if permanentValue > uint(^uint32(0)) {
		return nil, errors.New("upstream_availability.permanent_after_cycles is too large")
	}

	obj := &Obj{
		keyMap:          make(map[string]*keyMutObj, len(configObj.ReleaseMirrors)),
		diagnosticMap:   make(map[diagnosticKeyObj]*diagnosticRecordObj),
		diagnosticByKey: make(map[string]map[diagnosticKeyObj]struct{}),
		diagnosticOrder: make([]diagnosticKeyObj, 0),
		permanentAt:     uint32(permanentValue),
		maxDiagnostics:  cDefaultMaxDiagnostics,
		recentBuffer:    int(configObj.Metrics.Errors.RecentBuffer),
	}
	obj.selfObj = obj

	for key, sourceURL := range configObj.ReleaseMirrors {
		if err := validateText("key", key, cMaxKeyBytes); err != nil {
			return nil, err
		}
		if err := validateText(fmt.Sprintf("source URL for key %q", key), sourceURL, cMaxURLBytes); err != nil {
			return nil, err
		}
		key = strings.Clone(key)
		sourceURL = strings.Clone(sourceURL)
		obj.keyMap[key] = newKeyMutObj(key, sourceURL)
	}

	obj.buildKeyIndexLocked()
	obj.snapObj.Store(obj.buildSnapshotLocked())
	return obj, nil
}
