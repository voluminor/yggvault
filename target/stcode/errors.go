// Code generated using '_generate/errors'; DO NOT EDIT.
// Generation time: 2026-07-05T03:59:16Z

package stcode

import (
	"errors"
	"fmt"

	"github.com/rs/zerolog"
)

// // // // // // // // // //

type XErrInterface interface {
	error
	GetXErr(logger zerolog.Logger) *zerolog.Event
}

var (
	ccXErr_archive_limit_exceeded         = errors.New("archive_limit_exceeded")
	ccXErr_archive_rejected               = errors.New("archive_rejected")
	ccXErr_artifact_build_failed          = errors.New("artifact_build_failed")
	ccXErr_artifact_size_exceeded         = errors.New("artifact_size_exceeded")
	ccXErr_brother_blob_hash_mismatch     = errors.New("brother_blob_hash_mismatch")
	ccXErr_brother_contract_not_confirmed = errors.New("brother_contract_not_confirmed")
	ccXErr_brother_rpc_invalid            = errors.New("brother_rpc_invalid")
	ccXErr_cache_quota_exceeded           = errors.New("cache_quota_exceeded")
	ccXErr_composer_name_collision        = errors.New("composer_name_collision")
	ccXErr_source_download_failed         = errors.New("source_download_failed")
)

func buildXErrEvent(logger zerolog.Logger, baseErr error) *zerolog.Event {
	return logger.Err(baseErr).Str("err-name", baseErr.Error())
}

// // // // // //

type ErrArchiveLimitExceededObj struct {
	Cause    error
	Check    string
	Key      string
	MaxValue uint64
	Value    uint64
	Version  string
}

func NewErrArchiveLimitExceeded(cause error, check string, key string, maxValue uint64, value uint64, version string) *ErrArchiveLimitExceededObj {
	obj := &ErrArchiveLimitExceededObj{
		Cause:    cause,
		Check:    check,
		Key:      key,
		MaxValue: maxValue,
		Value:    value,
		Version:  version,
	}
	return obj
}

// //

func (obj *ErrArchiveLimitExceededObj) Error() string {
	return fmt.Sprintf("archive limit exceeded for key=%s version=%s: check=%s, value=%d, max_value=%d", obj.Key, obj.Version, obj.Check, obj.Value, obj.MaxValue)
}

func (obj *ErrArchiveLimitExceededObj) Unwrap() error {
	return obj.Cause
}

//

func (obj *ErrArchiveLimitExceededObj) GetXErr(logger zerolog.Logger) *zerolog.Event {
	event := buildXErrEvent(logger, ccXErr_archive_limit_exceeded)
	if obj.Cause != nil {
		event = event.Str("cause", obj.Cause.Error())
	}
	event = event.Str("check", obj.Check)
	event = event.Str("key", obj.Key)
	event = event.Uint64("max_value", obj.MaxValue)
	event = event.Uint64("value", obj.Value)
	event = event.Str("version", obj.Version)
	return event
}

// // // // // //

type ErrArchiveRejectedObj struct {
	Cause     error
	CheckName string
	Format    string
	Key       string
	Path      string
	Version   string
}

func NewErrArchiveRejected(cause error, checkName string, format string, key string, path string, version string) *ErrArchiveRejectedObj {
	obj := &ErrArchiveRejectedObj{
		Cause:     cause,
		CheckName: checkName,
		Format:    format,
		Key:       key,
		Path:      path,
		Version:   version,
	}
	return obj
}

// //

func (obj *ErrArchiveRejectedObj) Error() string {
	return fmt.Sprintf("source archive rejected for key=%s version=%s: check=%s", obj.Key, obj.Version, obj.CheckName)
}

func (obj *ErrArchiveRejectedObj) Unwrap() error {
	return obj.Cause
}

//

func (obj *ErrArchiveRejectedObj) GetXErr(logger zerolog.Logger) *zerolog.Event {
	event := buildXErrEvent(logger, ccXErr_archive_rejected)
	if obj.Cause != nil {
		event = event.Str("cause", obj.Cause.Error())
	}
	event = event.Str("check_name", obj.CheckName)
	event = event.Str("format", obj.Format)
	event = event.Str("key", obj.Key)
	event = event.Str("path", obj.Path)
	event = event.Str("version", obj.Version)
	return event
}

// // // // // //

type ErrArtifactBuildFailedObj struct {
	ActualHash     string
	ActualSize     uint64
	ArtifactKind   string
	Cause          error
	CheckName      string
	ExpectedHash   string
	ExpectedSize   uint64
	Key            string
	ListenerId     string
	MaterializerId string
	Version        string
}

func NewErrArtifactBuildFailed(actualHash string, actualSize uint64, artifactKind string, cause error, checkName string, expectedHash string, expectedSize uint64, key string, listenerId string, materializerId string, version string) *ErrArtifactBuildFailedObj {
	obj := &ErrArtifactBuildFailedObj{
		ActualHash:     actualHash,
		ActualSize:     actualSize,
		ArtifactKind:   artifactKind,
		Cause:          cause,
		CheckName:      checkName,
		ExpectedHash:   expectedHash,
		ExpectedSize:   expectedSize,
		Key:            key,
		ListenerId:     listenerId,
		MaterializerId: materializerId,
		Version:        version,
	}
	return obj
}

// //

func (obj *ErrArtifactBuildFailedObj) Error() string {
	return fmt.Sprintf("artifact materialization failed for key=%s version=%s: check=%s", obj.Key, obj.Version, obj.CheckName)
}

func (obj *ErrArtifactBuildFailedObj) Unwrap() error {
	return obj.Cause
}

//

func (obj *ErrArtifactBuildFailedObj) GetXErr(logger zerolog.Logger) *zerolog.Event {
	event := buildXErrEvent(logger, ccXErr_artifact_build_failed)
	event = event.Str("actual_hash", obj.ActualHash)
	event = event.Uint64("actual_size", obj.ActualSize)
	event = event.Str("artifact_kind", obj.ArtifactKind)
	if obj.Cause != nil {
		event = event.Str("cause", obj.Cause.Error())
	}
	event = event.Str("check_name", obj.CheckName)
	event = event.Str("expected_hash", obj.ExpectedHash)
	event = event.Uint64("expected_size", obj.ExpectedSize)
	event = event.Str("key", obj.Key)
	event = event.Str("listener_id", obj.ListenerId)
	event = event.Str("materializer_id", obj.MaterializerId)
	event = event.Str("version", obj.Version)
	return event
}

// // // // // //

type ErrArtifactSizeExceededObj struct {
	ArtifactKind   string
	CheckName      string
	Key            string
	ListenerId     string
	MaterializerId string
	MaxBytes       uint64
	SizeBytes      uint64
	Version        string
}

func NewErrArtifactSizeExceeded(artifactKind string, checkName string, key string, listenerId string, materializerId string, maxBytes uint64, sizeBytes uint64, version string) *ErrArtifactSizeExceededObj {
	obj := &ErrArtifactSizeExceededObj{
		ArtifactKind:   artifactKind,
		CheckName:      checkName,
		Key:            key,
		ListenerId:     listenerId,
		MaterializerId: materializerId,
		MaxBytes:       maxBytes,
		SizeBytes:      sizeBytes,
		Version:        version,
	}
	return obj
}

// //

func (obj *ErrArtifactSizeExceededObj) Error() string {
	return fmt.Sprintf("artifact size exceeds limit for key=%s version=%s: check=%s, size_bytes=%d, max_bytes=%d", obj.Key, obj.Version, obj.CheckName, obj.SizeBytes, obj.MaxBytes)
}

func (obj *ErrArtifactSizeExceededObj) Unwrap() error {
	return nil
}

//

func (obj *ErrArtifactSizeExceededObj) GetXErr(logger zerolog.Logger) *zerolog.Event {
	event := buildXErrEvent(logger, ccXErr_artifact_size_exceeded)
	event = event.Str("artifact_kind", obj.ArtifactKind)
	event = event.Str("check_name", obj.CheckName)
	event = event.Str("key", obj.Key)
	event = event.Str("listener_id", obj.ListenerId)
	event = event.Str("materializer_id", obj.MaterializerId)
	event = event.Uint64("max_bytes", obj.MaxBytes)
	event = event.Uint64("size_bytes", obj.SizeBytes)
	event = event.Str("version", obj.Version)
	return event
}

// // // // // //

type ErrBrotherBlobHashMismatchObj struct {
	ActualHash   string
	Cause        error
	ExpectedHash string
	Key          string
	SourceUrl    string
	Version      string
}

func NewErrBrotherBlobHashMismatch(actualHash string, cause error, expectedHash string, key string, sourceUrl string, version string) *ErrBrotherBlobHashMismatchObj {
	obj := &ErrBrotherBlobHashMismatchObj{
		ActualHash:   actualHash,
		Cause:        cause,
		ExpectedHash: expectedHash,
		Key:          key,
		SourceUrl:    sourceUrl,
		Version:      version,
	}
	return obj
}

// //

func (obj *ErrBrotherBlobHashMismatchObj) Error() string {
	return fmt.Sprintf("brother blob hash mismatch for key=%s version=%s: expected=%s, actual=%s", obj.Key, obj.Version, obj.ExpectedHash, obj.ActualHash)
}

func (obj *ErrBrotherBlobHashMismatchObj) Unwrap() error {
	return obj.Cause
}

//

func (obj *ErrBrotherBlobHashMismatchObj) GetXErr(logger zerolog.Logger) *zerolog.Event {
	event := buildXErrEvent(logger, ccXErr_brother_blob_hash_mismatch)
	event = event.Str("actual_hash", obj.ActualHash)
	if obj.Cause != nil {
		event = event.Str("cause", obj.Cause.Error())
	}
	event = event.Str("expected_hash", obj.ExpectedHash)
	event = event.Str("key", obj.Key)
	event = event.Str("source_url", obj.SourceUrl)
	event = event.Str("version", obj.Version)
	return event
}

// // // // // //

type ErrBrotherContractNotConfirmedObj struct {
	ContractReason string
	Key            string
	SourceUrl      string
}

func NewErrBrotherContractNotConfirmed(contractReason string, key string, sourceUrl string) *ErrBrotherContractNotConfirmedObj {
	obj := &ErrBrotherContractNotConfirmedObj{
		ContractReason: contractReason,
		Key:            key,
		SourceUrl:      sourceUrl,
	}
	return obj
}

// //

func (obj *ErrBrotherContractNotConfirmedObj) Error() string {
	return fmt.Sprintf("brother contract not confirmed for key=%s: reason=%s", obj.Key, obj.ContractReason)
}

func (obj *ErrBrotherContractNotConfirmedObj) Unwrap() error {
	return nil
}

//

func (obj *ErrBrotherContractNotConfirmedObj) GetXErr(logger zerolog.Logger) *zerolog.Event {
	event := buildXErrEvent(logger, ccXErr_brother_contract_not_confirmed)
	event = event.Str("contract_reason", obj.ContractReason)
	event = event.Str("key", obj.Key)
	event = event.Str("source_url", obj.SourceUrl)
	return event
}

// // // // // //

type ErrBrotherRpcInvalidObj struct {
	Cause         error
	Key           string
	MaxBytes      uint64
	Method        string
	ResponseBytes uint64
	RpcReason     string
	SourceUrl     string
}

func NewErrBrotherRpcInvalid(cause error, key string, maxBytes uint64, method string, responseBytes uint64, rpcReason string, sourceUrl string) *ErrBrotherRpcInvalidObj {
	obj := &ErrBrotherRpcInvalidObj{
		Cause:         cause,
		Key:           key,
		MaxBytes:      maxBytes,
		Method:        method,
		ResponseBytes: responseBytes,
		RpcReason:     rpcReason,
		SourceUrl:     sourceUrl,
	}
	return obj
}

// //

func (obj *ErrBrotherRpcInvalidObj) Error() string {
	return fmt.Sprintf("brother rpc invalid for key=%s: method=%s, reason=%s", obj.Key, obj.Method, obj.RpcReason)
}

func (obj *ErrBrotherRpcInvalidObj) Unwrap() error {
	return obj.Cause
}

//

func (obj *ErrBrotherRpcInvalidObj) GetXErr(logger zerolog.Logger) *zerolog.Event {
	event := buildXErrEvent(logger, ccXErr_brother_rpc_invalid)
	if obj.Cause != nil {
		event = event.Str("cause", obj.Cause.Error())
	}
	event = event.Str("key", obj.Key)
	event = event.Uint64("max_bytes", obj.MaxBytes)
	event = event.Str("method", obj.Method)
	event = event.Uint64("response_bytes", obj.ResponseBytes)
	event = event.Str("rpc_reason", obj.RpcReason)
	event = event.Str("source_url", obj.SourceUrl)
	return event
}

// // // // // //

type ErrCacheQuotaExceededObj struct {
	AdmissionBytes     uint64
	CacheArea          string
	Cause              error
	CheckName          string
	CurrentTotalBytes  uint64
	EvictToBytes       uint64
	IncomingBytes      uint64
	MaxTotalBytes      uint64
	RetainLatestPerKey uint32
}

func NewErrCacheQuotaExceeded(admissionBytes uint64, cacheArea string, cause error, checkName string, currentTotalBytes uint64, evictToBytes uint64, incomingBytes uint64, maxTotalBytes uint64, retainLatestPerKey uint32) *ErrCacheQuotaExceededObj {
	obj := &ErrCacheQuotaExceededObj{
		AdmissionBytes:     admissionBytes,
		CacheArea:          cacheArea,
		Cause:              cause,
		CheckName:          checkName,
		CurrentTotalBytes:  currentTotalBytes,
		EvictToBytes:       evictToBytes,
		IncomingBytes:      incomingBytes,
		MaxTotalBytes:      maxTotalBytes,
		RetainLatestPerKey: retainLatestPerKey,
	}
	return obj
}

// //

func (obj *ErrCacheQuotaExceededObj) Error() string {
	return fmt.Sprintf("cache quota exceeded: area=%s, check=%s, max_total_bytes=%d, current_total_bytes=%d, incoming_bytes=%d, admission_bytes=%d, evict_to_bytes=%d", obj.CacheArea, obj.CheckName, obj.MaxTotalBytes, obj.CurrentTotalBytes, obj.IncomingBytes, obj.AdmissionBytes, obj.EvictToBytes)
}

func (obj *ErrCacheQuotaExceededObj) Unwrap() error {
	return obj.Cause
}

//

func (obj *ErrCacheQuotaExceededObj) GetXErr(logger zerolog.Logger) *zerolog.Event {
	event := buildXErrEvent(logger, ccXErr_cache_quota_exceeded)
	event = event.Uint64("admission_bytes", obj.AdmissionBytes)
	event = event.Str("cache_area", obj.CacheArea)
	if obj.Cause != nil {
		event = event.Str("cause", obj.Cause.Error())
	}
	event = event.Str("check_name", obj.CheckName)
	event = event.Uint64("current_total_bytes", obj.CurrentTotalBytes)
	event = event.Uint64("evict_to_bytes", obj.EvictToBytes)
	event = event.Uint64("incoming_bytes", obj.IncomingBytes)
	event = event.Uint64("max_total_bytes", obj.MaxTotalBytes)
	event = event.Uint32("retain_latest_per_key", obj.RetainLatestPerKey)
	return event
}

// // // // // //

type ErrComposerNameCollisionObj struct {
	ConflictingKey string
	Key            string
	PackageName    string
}

func NewErrComposerNameCollision(conflictingKey string, key string, packageName string) *ErrComposerNameCollisionObj {
	obj := &ErrComposerNameCollisionObj{
		ConflictingKey: conflictingKey,
		Key:            key,
		PackageName:    packageName,
	}
	return obj
}

// //

func (obj *ErrComposerNameCollisionObj) Error() string {
	return fmt.Sprintf("composer package name collision for key=%s: name=%s already owned by key=%s", obj.Key, obj.PackageName, obj.ConflictingKey)
}

func (obj *ErrComposerNameCollisionObj) Unwrap() error {
	return nil
}

//

func (obj *ErrComposerNameCollisionObj) GetXErr(logger zerolog.Logger) *zerolog.Event {
	event := buildXErrEvent(logger, ccXErr_composer_name_collision)
	event = event.Str("conflicting_key", obj.ConflictingKey)
	event = event.Str("key", obj.Key)
	event = event.Str("package_name", obj.PackageName)
	return event
}

// // // // // //

type ErrSourceDownloadFailedObj struct {
	Attempts       uint8
	Cause          error
	Key            string
	SourceUrl      string
	TimeoutSeconds uint32
	Version        string
}

func NewErrSourceDownloadFailed(attempts uint8, cause error, key string, sourceUrl string, timeoutSeconds uint32, version string) *ErrSourceDownloadFailedObj {
	obj := &ErrSourceDownloadFailedObj{
		Attempts:       attempts,
		Cause:          cause,
		Key:            key,
		SourceUrl:      sourceUrl,
		TimeoutSeconds: timeoutSeconds,
		Version:        version,
	}
	return obj
}

// //

func (obj *ErrSourceDownloadFailedObj) Error() string {
	return fmt.Sprintf("source archive download failed for key=%s version=%s after %d attempts", obj.Key, obj.Version, obj.Attempts)
}

func (obj *ErrSourceDownloadFailedObj) Unwrap() error {
	return obj.Cause
}

//

func (obj *ErrSourceDownloadFailedObj) GetXErr(logger zerolog.Logger) *zerolog.Event {
	event := buildXErrEvent(logger, ccXErr_source_download_failed)
	event = event.Uint8("attempts", obj.Attempts)
	if obj.Cause != nil {
		event = event.Str("cause", obj.Cause.Error())
	}
	event = event.Str("key", obj.Key)
	event = event.Str("source_url", obj.SourceUrl)
	event = event.Uint32("timeout_seconds", obj.TimeoutSeconds)
	event = event.Str("version", obj.Version)
	return event
}
