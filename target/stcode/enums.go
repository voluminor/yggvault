// Code generated using '_generate/enums'; DO NOT EDIT.

package stcode

// // // // // // // // // //

const (
	ecs05fee125bad9 = "temporary_down"
	ecs0b4bd77cec70 = "brother"
	ecs2689367b205c = "ok"
	ecs273e7a665771 = "ygg"
	ecs2c70e12b7a06 = "key"
	ecs3c8cab8b47d6 = "degraded"
	ecs3f5e71edebda = "content_rejected"
	ecs42cc848a9f5e = "composer"
	ecs47bcd4e46fdf = "artifact_materialization_failed"
	ecs4b5e57f6eb2f = "web"
	ecs4cd0e21a9a07 = "go"
	ecs5ca4f3850ccc = "version"
	ecs60d69712a758 = "bad_request"
	ecs8001c2743965 = "global"
	ecs880813119264 = "upstream_unavailable"
	ecs99cedcf7866f = "permanent_down"
	ecs9a881b9b9f23 = "git"
	ecsa9fd07856242 = "universal"
	ecsb23a6a8439c0 = "unknown"
	ecsca00fccfb408 = "error"
	ecsddd9818abacf = "available"
	ecsde4814ba5913 = "go_overlay_degraded"
	ecse280f6b7de8c = "upstream_conflict"
	ecsf9bc3df196bc = "cache_quota_exceeded"
)

type EnumInterface interface {
	String() string
	Int() int
	IsValid() bool
}

// // // // // //

type AvailabilityStatusType uint8

const (
	UndefAvailabilityStatus         AvailabilityStatusType = 0
	AvailabilityStatusUnknown       AvailabilityStatusType = 1
	AvailabilityStatusAvailable     AvailabilityStatusType = 2
	AvailabilityStatusTemporaryDown AvailabilityStatusType = 3
	AvailabilityStatusPermanentDown AvailabilityStatusType = 4
)

var availabilityStatusToStringMap = map[AvailabilityStatusType]string{
	AvailabilityStatusUnknown:       ecsb23a6a8439c0,
	AvailabilityStatusAvailable:     ecsddd9818abacf,
	AvailabilityStatusTemporaryDown: ecs05fee125bad9,
	AvailabilityStatusPermanentDown: ecs99cedcf7866f,
}

var availabilityStatusFromStringMap = map[string]AvailabilityStatusType{
	ecsb23a6a8439c0: AvailabilityStatusUnknown,
	ecsddd9818abacf: AvailabilityStatusAvailable,
	ecs05fee125bad9: AvailabilityStatusTemporaryDown,
	ecs99cedcf7866f: AvailabilityStatusPermanentDown,
}

// //

func (v AvailabilityStatusType) String() string {
	text, ok := availabilityStatusToStringMap[v]
	if !ok {
		return ""
	}
	return text
}

func (v AvailabilityStatusType) Int() int {
	return int(v)
}

func (v AvailabilityStatusType) IsValid() bool {
	return uint8(v) >= 1 && uint8(v) <= uint8(4)
}

//

func ParseAvailabilityStatusFromString(value string) (AvailabilityStatusType, bool) {
	v, ok := availabilityStatusFromStringMap[value]
	return v, ok
}

func ParseAvailabilityStatusFromUint8(value uint8) (AvailabilityStatusType, bool) {
	v := AvailabilityStatusType(value)
	return v, v.IsValid()
}

func ParseAvailabilityStatusFromInt(value int) (AvailabilityStatusType, bool) {
	if value < 0 || value > 255 {
		return UndefAvailabilityStatus, false
	}
	return ParseAvailabilityStatusFromUint8(uint8(value))
}

func AllAvailabilityStatus() []AvailabilityStatusType {
	return []AvailabilityStatusType{
		UndefAvailabilityStatus,
		AvailabilityStatusUnknown,
		AvailabilityStatusAvailable,
		AvailabilityStatusTemporaryDown,
		AvailabilityStatusPermanentDown,
	}
}

// // // // // //

type EcosystemType uint8

const (
	UndefEcosystem    EcosystemType = 0
	EcosystemGo       EcosystemType = 1
	EcosystemComposer EcosystemType = 2
)

var ecosystemToStringMap = map[EcosystemType]string{
	EcosystemGo:       ecs4cd0e21a9a07,
	EcosystemComposer: ecs42cc848a9f5e,
}

var ecosystemFromStringMap = map[string]EcosystemType{
	ecs4cd0e21a9a07: EcosystemGo,
	ecs42cc848a9f5e: EcosystemComposer,
}

// //

func (v EcosystemType) String() string {
	text, ok := ecosystemToStringMap[v]
	if !ok {
		return ""
	}
	return text
}

func (v EcosystemType) Int() int {
	return int(v)
}

func (v EcosystemType) IsValid() bool {
	return uint8(v) >= 1 && uint8(v) <= uint8(2)
}

//

func ParseEcosystemFromString(value string) (EcosystemType, bool) {
	v, ok := ecosystemFromStringMap[value]
	return v, ok
}

func ParseEcosystemFromUint8(value uint8) (EcosystemType, bool) {
	v := EcosystemType(value)
	return v, v.IsValid()
}

func ParseEcosystemFromInt(value int) (EcosystemType, bool) {
	if value < 0 || value > 255 {
		return UndefEcosystem, false
	}
	return ParseEcosystemFromUint8(uint8(value))
}

func AllEcosystem() []EcosystemType {
	return []EcosystemType{
		UndefEcosystem,
		EcosystemGo,
		EcosystemComposer,
	}
}

// // // // // //

type ListenerType uint8

const (
	UndefListener  ListenerType = 0
	ListenerGlobal ListenerType = 1
	ListenerWeb    ListenerType = 2
	ListenerYgg    ListenerType = 3
)

var listenerToStringMap = map[ListenerType]string{
	ListenerGlobal: ecs8001c2743965,
	ListenerWeb:    ecs4b5e57f6eb2f,
	ListenerYgg:    ecs273e7a665771,
}

var listenerFromStringMap = map[string]ListenerType{
	ecs8001c2743965: ListenerGlobal,
	ecs4b5e57f6eb2f: ListenerWeb,
	ecs273e7a665771: ListenerYgg,
}

// //

func (v ListenerType) String() string {
	text, ok := listenerToStringMap[v]
	if !ok {
		return ""
	}
	return text
}

func (v ListenerType) Int() int {
	return int(v)
}

func (v ListenerType) IsValid() bool {
	return uint8(v) >= 1 && uint8(v) <= uint8(3)
}

//

func ParseListenerFromString(value string) (ListenerType, bool) {
	v, ok := listenerFromStringMap[value]
	return v, ok
}

func ParseListenerFromUint8(value uint8) (ListenerType, bool) {
	v := ListenerType(value)
	return v, v.IsValid()
}

func ParseListenerFromInt(value int) (ListenerType, bool) {
	if value < 0 || value > 255 {
		return UndefListener, false
	}
	return ParseListenerFromUint8(uint8(value))
}

func AllListener() []ListenerType {
	return []ListenerType{
		UndefListener,
		ListenerGlobal,
		ListenerWeb,
		ListenerYgg,
	}
}

// // // // // //

type LogReasonType uint8

const (
	UndefLogReason                         LogReasonType = 0
	LogReasonBadRequest                    LogReasonType = 1
	LogReasonUpstreamUnavailable           LogReasonType = 2
	LogReasonUpstreamConflict              LogReasonType = 3
	LogReasonCacheQuotaExceeded            LogReasonType = 4
	LogReasonArtifactMaterializationFailed LogReasonType = 5
	LogReasonGoOverlayDegraded             LogReasonType = 6
	LogReasonContentRejected               LogReasonType = 7
)

var logReasonToStringMap = map[LogReasonType]string{
	LogReasonBadRequest:                    ecs60d69712a758,
	LogReasonUpstreamUnavailable:           ecs880813119264,
	LogReasonUpstreamConflict:              ecse280f6b7de8c,
	LogReasonCacheQuotaExceeded:            ecsf9bc3df196bc,
	LogReasonArtifactMaterializationFailed: ecs47bcd4e46fdf,
	LogReasonGoOverlayDegraded:             ecsde4814ba5913,
	LogReasonContentRejected:               ecs3f5e71edebda,
}

var logReasonFromStringMap = map[string]LogReasonType{
	ecs60d69712a758: LogReasonBadRequest,
	ecs880813119264: LogReasonUpstreamUnavailable,
	ecse280f6b7de8c: LogReasonUpstreamConflict,
	ecsf9bc3df196bc: LogReasonCacheQuotaExceeded,
	ecs47bcd4e46fdf: LogReasonArtifactMaterializationFailed,
	ecsde4814ba5913: LogReasonGoOverlayDegraded,
	ecs3f5e71edebda: LogReasonContentRejected,
}

// //

func (v LogReasonType) String() string {
	text, ok := logReasonToStringMap[v]
	if !ok {
		return ""
	}
	return text
}

func (v LogReasonType) Int() int {
	return int(v)
}

func (v LogReasonType) IsValid() bool {
	return uint8(v) >= 1 && uint8(v) <= uint8(7)
}

//

func ParseLogReasonFromString(value string) (LogReasonType, bool) {
	v, ok := logReasonFromStringMap[value]
	return v, ok
}

func ParseLogReasonFromUint8(value uint8) (LogReasonType, bool) {
	v := LogReasonType(value)
	return v, v.IsValid()
}

func ParseLogReasonFromInt(value int) (LogReasonType, bool) {
	if value < 0 || value > 255 {
		return UndefLogReason, false
	}
	return ParseLogReasonFromUint8(uint8(value))
}

func AllLogReason() []LogReasonType {
	return []LogReasonType{
		UndefLogReason,
		LogReasonBadRequest,
		LogReasonUpstreamUnavailable,
		LogReasonUpstreamConflict,
		LogReasonCacheQuotaExceeded,
		LogReasonArtifactMaterializationFailed,
		LogReasonGoOverlayDegraded,
		LogReasonContentRejected,
	}
}

// // // // // //

type LogScopeType uint8

const (
	UndefLogScope   LogScopeType = 0
	LogScopeGlobal  LogScopeType = 1
	LogScopeKey     LogScopeType = 2
	LogScopeVersion LogScopeType = 3
)

var logScopeToStringMap = map[LogScopeType]string{
	LogScopeGlobal:  ecs8001c2743965,
	LogScopeKey:     ecs2c70e12b7a06,
	LogScopeVersion: ecs5ca4f3850ccc,
}

var logScopeFromStringMap = map[string]LogScopeType{
	ecs8001c2743965: LogScopeGlobal,
	ecs2c70e12b7a06: LogScopeKey,
	ecs5ca4f3850ccc: LogScopeVersion,
}

// //

func (v LogScopeType) String() string {
	text, ok := logScopeToStringMap[v]
	if !ok {
		return ""
	}
	return text
}

func (v LogScopeType) Int() int {
	return int(v)
}

func (v LogScopeType) IsValid() bool {
	return uint8(v) >= 1 && uint8(v) <= uint8(3)
}

//

func ParseLogScopeFromString(value string) (LogScopeType, bool) {
	v, ok := logScopeFromStringMap[value]
	return v, ok
}

func ParseLogScopeFromUint8(value uint8) (LogScopeType, bool) {
	v := LogScopeType(value)
	return v, v.IsValid()
}

func ParseLogScopeFromInt(value int) (LogScopeType, bool) {
	if value < 0 || value > 255 {
		return UndefLogScope, false
	}
	return ParseLogScopeFromUint8(uint8(value))
}

func AllLogScope() []LogScopeType {
	return []LogScopeType{
		UndefLogScope,
		LogScopeGlobal,
		LogScopeKey,
		LogScopeVersion,
	}
}

// // // // // //

type MaterializerType uint8

const (
	UndefMaterializer     MaterializerType = 0
	MaterializerUniversal MaterializerType = 1
	MaterializerGo        MaterializerType = 2
)

var materializerToStringMap = map[MaterializerType]string{
	MaterializerUniversal: ecsa9fd07856242,
	MaterializerGo:        ecs4cd0e21a9a07,
}

var materializerFromStringMap = map[string]MaterializerType{
	ecsa9fd07856242: MaterializerUniversal,
	ecs4cd0e21a9a07: MaterializerGo,
}

// //

func (v MaterializerType) String() string {
	text, ok := materializerToStringMap[v]
	if !ok {
		return ""
	}
	return text
}

func (v MaterializerType) Int() int {
	return int(v)
}

func (v MaterializerType) IsValid() bool {
	return uint8(v) >= 1 && uint8(v) <= uint8(2)
}

//

func ParseMaterializerFromString(value string) (MaterializerType, bool) {
	v, ok := materializerFromStringMap[value]
	return v, ok
}

func ParseMaterializerFromUint8(value uint8) (MaterializerType, bool) {
	v := MaterializerType(value)
	return v, v.IsValid()
}

func ParseMaterializerFromInt(value int) (MaterializerType, bool) {
	if value < 0 || value > 255 {
		return UndefMaterializer, false
	}
	return ParseMaterializerFromUint8(uint8(value))
}

func AllMaterializer() []MaterializerType {
	return []MaterializerType{
		UndefMaterializer,
		MaterializerUniversal,
		MaterializerGo,
	}
}

// // // // // //

type OperationalStatusType uint8

const (
	UndefOperationalStatus    OperationalStatusType = 0
	OperationalStatusOk       OperationalStatusType = 1
	OperationalStatusDegraded OperationalStatusType = 2
	OperationalStatusError    OperationalStatusType = 3
)

var operationalStatusToStringMap = map[OperationalStatusType]string{
	OperationalStatusOk:       ecs2689367b205c,
	OperationalStatusDegraded: ecs3c8cab8b47d6,
	OperationalStatusError:    ecsca00fccfb408,
}

var operationalStatusFromStringMap = map[string]OperationalStatusType{
	ecs2689367b205c: OperationalStatusOk,
	ecs3c8cab8b47d6: OperationalStatusDegraded,
	ecsca00fccfb408: OperationalStatusError,
}

// //

func (v OperationalStatusType) String() string {
	text, ok := operationalStatusToStringMap[v]
	if !ok {
		return ""
	}
	return text
}

func (v OperationalStatusType) Int() int {
	return int(v)
}

func (v OperationalStatusType) IsValid() bool {
	return uint8(v) >= 1 && uint8(v) <= uint8(3)
}

//

func ParseOperationalStatusFromString(value string) (OperationalStatusType, bool) {
	v, ok := operationalStatusFromStringMap[value]
	return v, ok
}

func ParseOperationalStatusFromUint8(value uint8) (OperationalStatusType, bool) {
	v := OperationalStatusType(value)
	return v, v.IsValid()
}

func ParseOperationalStatusFromInt(value int) (OperationalStatusType, bool) {
	if value < 0 || value > 255 {
		return UndefOperationalStatus, false
	}
	return ParseOperationalStatusFromUint8(uint8(value))
}

func AllOperationalStatus() []OperationalStatusType {
	return []OperationalStatusType{
		UndefOperationalStatus,
		OperationalStatusOk,
		OperationalStatusDegraded,
		OperationalStatusError,
	}
}

// // // // // //

type SourceClassType uint8

const (
	UndefSourceClass   SourceClassType = 0
	SourceClassGit     SourceClassType = 1
	SourceClassBrother SourceClassType = 2
)

var sourceClassToStringMap = map[SourceClassType]string{
	SourceClassGit:     ecs9a881b9b9f23,
	SourceClassBrother: ecs0b4bd77cec70,
}

var sourceClassFromStringMap = map[string]SourceClassType{
	ecs9a881b9b9f23: SourceClassGit,
	ecs0b4bd77cec70: SourceClassBrother,
}

// //

func (v SourceClassType) String() string {
	text, ok := sourceClassToStringMap[v]
	if !ok {
		return ""
	}
	return text
}

func (v SourceClassType) Int() int {
	return int(v)
}

func (v SourceClassType) IsValid() bool {
	return uint8(v) >= 1 && uint8(v) <= uint8(2)
}

//

func ParseSourceClassFromString(value string) (SourceClassType, bool) {
	v, ok := sourceClassFromStringMap[value]
	return v, ok
}

func ParseSourceClassFromUint8(value uint8) (SourceClassType, bool) {
	v := SourceClassType(value)
	return v, v.IsValid()
}

func ParseSourceClassFromInt(value int) (SourceClassType, bool) {
	if value < 0 || value > 255 {
		return UndefSourceClass, false
	}
	return ParseSourceClassFromUint8(uint8(value))
}

func AllSourceClass() []SourceClassType {
	return []SourceClassType{
		UndefSourceClass,
		SourceClassGit,
		SourceClassBrother,
	}
}
