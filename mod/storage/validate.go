package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/osfs"
	"github.com/voluminor/yggvault/mod/internal/util"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

// ErrInvalidRef marks malformed key/version input that callers map to 400.
var ErrInvalidRef = errors.New("invalid reference")

// //

func validateKey(key string) error {
	if !keyPatternObj.MatchString(key) {
		return fmt.Errorf("invalid key %q: %w", key, ErrInvalidRef)
	}
	if util.HasWindowsReservedBase(key) {
		return fmt.Errorf("key %q uses a Windows reserved name: %w", key, ErrInvalidRef)
	}
	return nil
}

func validateVersion(version string) error {
	if !util.IsStorableSemver(version) && !util.IsStorableRawVersion(version) {
		return fmt.Errorf("version %q must be a strict semver or a storable raw name: %w", version, ErrInvalidRef)
	}
	return nil
}

func validateUpstreamRef(ref string) error {
	if ref == "" {
		return nil
	}
	if len(ref) != 40 && len(ref) != 64 {
		return fmt.Errorf("upstream ref %q must be a 40- or 64-char hex sha: %w", ref, ErrInvalidRef)
	}
	for i := 0; i < len(ref); i++ {
		symbolByte := ref[i]
		if (symbolByte < '0' || symbolByte > '9') && (symbolByte < 'a' || symbolByte > 'f') {
			return fmt.Errorf("upstream ref %q must be lowercase hex: %w", ref, ErrInvalidRef)
		}
	}
	return nil
}

func validateSmallID(field string, value string) error {
	if !smallIDPatternObj.MatchString(value) {
		return fmt.Errorf("%s is invalid: %q", field, value)
	}
	return nil
}

func validatePathID(field string, value string) error {
	if err := validateSmallID(field, value); err != nil {
		return err
	}
	if util.HasWindowsReservedBase(value) {
		return fmt.Errorf("%s uses a Windows reserved name: %q", field, value)
	}
	return nil
}

func validateListener(listenerID string) string {
	value := strings.TrimSpace(listenerID)
	if value == "" {
		return cListenerGlobal
	}
	return value
}

func validateArtifactKey(keyObj core.ArtifactKeyObj) (core.ArtifactKeyObj, error) {
	keyObj.ListenerID = validateListener(keyObj.ListenerID)
	for _, itemObj := range []struct {
		field string
		value string
	}{
		{"materializer_id", keyObj.MaterializerID},
		{"artifact_kind", keyObj.ArtifactKind},
		{"listener_id", keyObj.ListenerID},
	} {
		if err := validatePathID(itemObj.field, itemObj.value); err != nil {
			return keyObj, err
		}
	}
	if err := validateKey(keyObj.Key); err != nil {
		return keyObj, err
	}
	if err := validateVersion(keyObj.Version); err != nil {
		return keyObj, err
	}
	return keyObj, nil
}

func validateEntryMode(mode string) (string, error) {
	switch mode {
	case "", cModeFile:
		return cModeFile, nil
	case cModeSymlink:
		return cModeSymlink, nil
	default:
		return "", fmt.Errorf("unsupported entry mode: %q", mode)
	}
}

func validateEntryPath(path string, maxPathBytes uint) (string, error) {
	if err := util.ValidateArchiveEntryPath(path, maxPathBytes); err != nil {
		return "", err
	}
	return util.CleanArchiveEntryPath(path)
}

const cMaxBlobBytesHardLimit uint64 = 256 << 20

func (obj *Obj) maxBlobBytes() uint64 {
	configMax := uint64(obj.configObj.Storage.ArchiveLimits.Size.PerFile)
	if configMax == 0 || configMax > cMaxBlobBytesHardLimit {
		configMax = cMaxBlobBytesHardLimit
	}
	if ramBytes := osfs.SystemMemoryBytes(); ramBytes > 0 && configMax > ramBytes {
		return ramBytes
	}
	return configMax
}

func validateMemoryBudget(configObj *stcfg.ConfigObj) error {
	ramBytes := osfs.SystemMemoryBytes()
	if ramBytes == 0 {
		return nil
	}
	configMax := uint64(configObj.Storage.ArchiveLimits.Size.PerFile)
	if configMax > ramBytes {
		return fmt.Errorf("storage.archive_limits.size.per_file (%d) exceeds system memory (%d): a blob is materialized in RAM whole", configMax, ramBytes)
	}
	return nil
}

func validateMaxBytes(field string, value string, maxBytes int) error {
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds maximum size of %d bytes", field, maxBytes)
	}
	return nil
}

func validateEvidenceJSON(text string) (string, error) {
	value := strings.TrimSpace(text)
	if value == "" {
		value = "{}"
	}
	if err := validateMaxBytes("evidence_json", value, cMaxEvidenceBytes); err != nil {
		return "", err
	}
	if !json.Valid([]byte(value)) {
		return "", errors.New("evidence_json must be valid JSON")
	}
	return value, nil
}

func validateEventType(text string) (string, error) {
	value := strings.TrimSpace(text)
	if value == "" {
		value = "publish"
	}
	if err := validateMaxBytes("event_type", value, cMaxEventTypeBytes); err != nil {
		return "", err
	}
	if err := validateSmallID("event_type", value); err != nil {
		return "", err
	}
	return value, nil
}

func validateEventMessage(text string) error {
	return validateMaxBytes("event message", text, cMaxEventMessageBytes)
}

func validateReleaseNotes(text string) error {
	return validateMaxBytes("release notes", text, cMaxReleaseNotesBytes)
}

func validateETag(text string) error {
	if err := validateMaxBytes("etag", text, cMaxETagBytes); err != nil {
		return err
	}
	if strings.ContainsAny(text, "\r\n") {
		return errors.New("etag contains line break")
	}
	return nil
}

func validateDegradedReason(text string) error {
	return validateMaxBytes("degraded_reason", text, cMaxDegradedReasonBytes)
}

func beginOperation(obj *Obj) (func(), error) {
	if obj == nil {
		return nil, errors.New("storage is nil")
	}
	obj.closeMu.Lock()
	if obj.closedFlag {
		obj.closeMu.Unlock()
		return nil, errors.New("storage is closed")
	}
	obj.activeWG.Add(1)
	obj.closeMu.Unlock()
	return obj.activeWG.Done, nil
}
