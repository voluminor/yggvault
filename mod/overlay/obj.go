package overlay

import (
	"context"
	"io"
	"strings"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	// cMaxManifestBytes caps manifest parsing size for go.mod/composer.json. Ingest already enforces per-file caps;
	// this is overlay defense in depth.
	cMaxManifestBytes = 1 << 20

	// cMaxComposerNameBytes caps composer names that enter routes and file names.
	cMaxComposerNameBytes = 150

	// Format versions for byte-stable materializers. Increment on byte output changes to invalidate rebuild body_hash
	// checks. Composer/Go served-live JSON metadata is not included here.

	// GoZipFormatVersion is the Go module zip format version.
	GoZipFormatVersion = 2
	// UniversalZipFormatVersion is the universal zip format version.
	UniversalZipFormatVersion = 2
	// UniversalTarGzFormatVersion is the universal tar.gz format version.
	UniversalTarGzFormatVersion = 2
)

// // // // // // // // // //

// StorageInterface is the narrow storage read boundary required by overlay builders.
type StorageInterface interface {
	ReadTree(ctx context.Context, treeHashObj core.HashObj) ([]core.TreeEntryObj, error)
	ReadBlob(ctx context.Context, hashObj core.HashObj) ([]byte, error)
}

// ArtifactBuilderInterface matches storage.ArtifactBuilderInterface structurally so overlay can pass builders to
// storage without importing the storage root.
type ArtifactBuilderInterface interface {
	Build(ctx context.Context, writerObj io.Writer) error
}

// ListenerCtxObj is listener host context for host-sensitive Go output.
type ListenerCtxObj struct {
	ListenerID  stcode.ListenerType
	EntryHost   string
	RoutePrefix string
}

// ListenerContexts returns host contexts for active listeners. It is the shared source for rescan materialization and
// rebuild/self-test so host-sensitive artifacts are rebuilt under the same EntryHost used for serving.
func ListenerContexts(domain string, routePrefix string, yggHost string) []ListenerCtxObj {
	listenerArr := []ListenerCtxObj{
		{ListenerID: stcode.ListenerWeb, EntryHost: domain, RoutePrefix: routePrefix},
	}
	if yggHost != "" {
		listenerArr = append(listenerArr, ListenerCtxObj{ListenerID: stcode.ListenerYgg, EntryHost: yggHost, RoutePrefix: routePrefix})
	}
	return listenerArr
}

// // // // // // // // // //

// Obj is the overlay registry: ordered detectors, config-derived parameters, and archive engine.
// It is immutable after New so router tables can stay strict and fixed.
type Obj struct {
	detectors      []DetectorInterface
	archiveObj     *archive.Obj
	rewriteEnabled bool
	domain         string
	routingPrefix  string
	nested         bool
	maxFileBytes   uint64
}

// // // // // // // // // //

// New builds the registry from config and holds no network or disk resources.
func New(configObj *stconf.ConfigObj) (*Obj, error) {
	archiveObj, err := archive.New(archive.LimitsObj{
		MaxArchiveSize:         uint64(configObj.Storage.ArchiveLimits.Size.Compressed),
		MaxArchiveUnpackedSize: uint64(configObj.Storage.ArchiveLimits.Size.Unpacked),
		MaxArchiveFileBytes:    uint64(configObj.Storage.ArchiveLimits.Size.PerFile),
		MaxArchiveFiles:        configObj.Storage.ArchiveLimits.Entries.Count,
		MaxArchivePathBytes:    configObj.Storage.ArchiveLimits.Entries.PathBytes,
	})
	if err != nil {
		return nil, err
	}

	return &Obj{
		detectors: []DetectorInterface{
			goDetectorObj{},
			composerDetectorObj{},
		},
		archiveObj:     archiveObj,
		rewriteEnabled: configObj.Overlay.Go.RewriteEnabled,
		domain:         configObj.Web.Server.Domain,
		routingPrefix:  configObj.Web.Routing.Prefix,
		nested:         configObj.Web.Static.Dir != "",
		maxFileBytes:   uint64(configObj.Storage.ArchiveLimits.Size.PerFile),
	}, nil
}

func validRefSegment(text string) bool {
	if text == "" || len(text) > 256 {
		return false
	}
	if text == "." || text == ".." || strings.Contains(text, "/") || strings.Contains(text, "\\") {
		return false
	}
	for _, r := range text {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}
