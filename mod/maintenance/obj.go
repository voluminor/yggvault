package maintenance

// // // // // // // // // //

const (
	cCommandInspect      = "inspect"
	cCommandPrune        = "prune"
	cCommandVacuum       = "vacuum"
	cCommandRebuildCache = "rebuild-cache"
	cCommandMaintenance  = "maintenance"
)

// // // // // // // // // //

// RequestObj describes one maintenance command without depending on the CLI layer.
type RequestObj struct {
	Inspect      bool
	Prune        bool
	Vacuum       bool
	RebuildCache bool
	Force        bool
	JsonOutput   bool
}

// CommandName returns the stable command name for output and the JSON envelope.
func (obj RequestObj) CommandName() string {
	switch {
	case obj.Inspect:
		return cCommandInspect
	case obj.Vacuum:
		return cCommandVacuum
	case obj.Prune:
		return cCommandPrune
	case obj.RebuildCache:
		return cCommandRebuildCache
	default:
		return cCommandMaintenance
	}
}

// ReportedErrObj marks an error already rendered for the operator.
type ReportedErrObj struct {
	Err error
}

func (obj ReportedErrObj) Error() string {
	if obj.Err == nil {
		return "maintenance error already reported"
	}
	return obj.Err.Error()
}

func (obj ReportedErrObj) Unwrap() error {
	return obj.Err
}

// ArtifactDriftObj describes a drift of one materialized artifact.
type ArtifactDriftObj struct {
	Key            string `json:"key"`
	Version        string `json:"version"`
	MaterializerID string `json:"materializer_id"`
	ArtifactKind   string `json:"artifact_kind"`
	ListenerID     string `json:"listener_id"`
	FormatVersion  uint32 `json:"format_version"`
	StoredHash     string `json:"stored_hash"`
	RebuiltHash    string `json:"rebuilt_hash"`
}

// RebuildResultObj aggregates the result of a full artifact verification.
type RebuildResultObj struct {
	Scanned uint64
	Drift   uint64
	Updated uint64
	Created uint64
	Pruned  uint64
	Items   []ArtifactDriftObj
}

// KeySourceVerdictObj explains a conflict between config release_mirrors and the persisted source binding.
type KeySourceVerdictObj struct {
	Key    string
	Code   string
	Detail string
}
