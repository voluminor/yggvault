package server

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/mesh"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/server/artifactio"
	"github.com/voluminor/yggvault/mod/server/dataapi"
	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/mod/server/serr"
	"github.com/voluminor/yggvault/mod/server/webui"
	"github.com/voluminor/yggvault/mod/view"
	"github.com/voluminor/yggvault/target"
	"github.com/voluminor/yggvault/target/api"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

type funcObj struct {
	deps          DepsObj
	objCache      *objCacheObj           // RAM cache for typed bodies: composer-p2 and go-latest
	assets        *assetSnapshotObj      // favicon + whitelisted logos pre-rendered once at startup
	contactGroups []view.ContactGroupObj // sorted contacts from immutable config
	edgeMetrics   *edgeMetricsObj
}

var _ api.FuncInterface = (*funcObj)(nil)

// // // // // // // // // //

func mapHealthStatus(statusObj stcode.OperationalStatusType) api.HealthObjStatus {
	switch statusObj {
	case stcode.OperationalStatusOk:
		return api.HealthObjStatusOk
	case stcode.OperationalStatusDegraded:
		return api.HealthObjStatusDegraded
	default:
		return api.HealthObjStatusError
	}
}

func reasonStrings(reasonArr []stcode.LogReasonType) []string {
	if len(reasonArr) == 0 {
		return nil
	}
	outArr := make([]string, 0, len(reasonArr))
	for _, reasonObj := range reasonArr {
		outArr = append(outArr, reasonObj.String())
	}
	return outArr
}

func (obj *funcObj) effectivePrefix() string {
	if obj.deps.Config.Web.Static.Dir == "" {
		return ""
	}
	return obj.deps.Config.Web.Routing.Prefix
}

func (obj *funcObj) linkCtx(lc listenerCtxObj) link.Obj {
	return link.Obj{
		Scheme:      lc.scheme(),
		EntryHost:   lc.entryHost,
		RoutePrefix: obj.effectivePrefix(),
	}
}

func (obj *funcObj) overlayListenerCtx(lc listenerCtxObj) overlay.ListenerCtxObj {
	listenerID := lc.listenerID
	if listenerID == 0 {
		listenerID = stcode.ListenerWeb
	}
	return overlay.ListenerCtxObj{
		ListenerID:  listenerID,
		EntryHost:   lc.entryHost,
		RoutePrefix: obj.effectivePrefix(),
	}
}

func (obj *funcObj) listPageSize() int {
	return max(1, int(obj.deps.Config.Web.Pages.ListPageSize))
}

// // // // // // // // // //

type overlayWebObj struct {
	overlayObj *overlay.Obj
	listenerLC overlay.ListenerCtxObj
}

// GoPublishable reports whether the version is published as a Go module for the listener host context.
func (obj overlayWebObj) GoPublishable(key string, version string, detectionObj core.DetectionObj, candidateObj *overlay.CandidateObj) bool {
	return obj.overlayObj.GoPublishable(key, version, detectionObj, candidateObj, obj.listenerLC)
}

// TargetModulePath returns the host-dependent module path for the version.
func (obj overlayWebObj) TargetModulePath(key string, version string) string {
	return obj.overlayObj.TargetModulePath(key, version, obj.listenerLC)
}

// UniversalTopDir returns the top directory of the universal archive for the listener host context.
func (obj overlayWebObj) UniversalTopDir(key string, version string, detectionObj core.DetectionObj, candidateObj *overlay.CandidateObj) string {
	return obj.overlayObj.UniversalTopDir(key, version, detectionObj, candidateObj, obj.listenerLC)
}

func (obj *funcObj) overlayWeb(lc listenerCtxObj) overlayWebObj {
	return overlayWebObj{overlayObj: obj.deps.Overlay, listenerLC: obj.overlayListenerCtx(lc)}
}

// // // // // // // // // //

// GetHealth serves the live status of the entry channel without cache or ETag.
func (obj *funcObj) GetHealth(ctx context.Context) (api.GetHealthRes, error) {
	lc := listenerCtxFrom(ctx)
	healthView := obj.deps.State.Health()
	snapshotObj := obj.deps.State.Snapshot()

	healthObj := api.HealthObj{
		Status:  mapHealthStatus(healthView.Status),
		Reasons: reasonStrings(healthView.Reasons),
		Service: api.HealthObjServiceYggvault,
		Source:  lc.healthSource,
		Ts:      time.Now().UTC(),
	}
	healthObj.ErrorsCount = api.NewOptInt32(int32(healthView.DiagnosticsCount))
	if !snapshotObj.LastRescan.IsZero() {
		healthObj.LastRescan = api.NewOptDateTime(snapshotObj.LastRescan.UTC())
	}
	return &api.HealthObjHeaders{CacheControl: api.NewOptString(cNoCacheControl), Response: healthObj}, nil
}

// GetInfo serves the node's public card: name, description, contacts and public addresses.
// An empty address means the entry is disabled; the response is always no-cache.
func (obj *funcObj) GetInfo(ctx context.Context) (api.GetInfoRes, error) {
	domain := obj.deps.Config.Web.Server.Domain
	yggHost := ""
	if obj.deps.Mesh != nil && obj.deps.Mesh.Enabled() {
		yggHost = obj.deps.Mesh.Host()
	}
	infoObj := api.InfoObj{
		Name:        mesh.ResolveName(obj.deps.Config.Info.Name, domain, yggHost),
		Version:     api.NewOptString(target.Version),
		Domain:      domain,
		YggHost:     yggHost,
		RoutePrefix: api.NewOptString(obj.effectivePrefix()),
	}
	if len(obj.deps.Config.Info.Contacts) > 0 {
		infoObj.Contacts = api.NewOptInfoObjContacts(api.InfoObjContacts(obj.deps.Config.Info.Contacts))
	}
	if obj.deps.Config.Info.Description != "" {
		infoObj.Description = api.NewOptString(obj.deps.Config.Info.Description)
	}
	if obj.deps.Config.Info.Location != "" {
		infoObj.Location = api.NewOptString(obj.deps.Config.Info.Location)
	}
	return &api.InfoObjHeaders{CacheControl: api.NewOptString(cNoCacheControl), Response: infoObj}, nil
}

// // // // // // // // // //
// Data API operations build bodies in dataapi; funcObj owns cache, ETag and conditional headers.

// GetCatalog serves `/catalog.json`, a cached cross-key snapshot.
// The body does not depend on the host, so a single byte-cache entry serves all listeners.
func (obj *funcObj) GetCatalog(ctx context.Context, params api.GetCatalogParams) (api.GetCatalogRes, error) {
	lc := listenerCtxFrom(ctx)
	etag := etagOf("catalog", obj.crossFreshness())
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	body, err := obj.cachedBytes(ctx, etag, func(buildCtx context.Context) ([]byte, error) {
		catalogObj := dataapi.BuildCatalog(obj.deps.State, obj.linkCtx(lc))
		return encodeJSON(&catalogObj), nil
	})
	if err != nil {
		return nil, err
	}
	return &api.GetCatalogOKTextPlainHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     api.GetCatalogOKTextPlain{Data: bytes.NewReader(body)},
	}, nil
}

// GetKeyReleases serves `/{key}/releases.json`, a cached paginated release list.
// A bad cursor yields 400, an unknown key 404; the body does not depend on the host.
func (obj *funcObj) GetKeyReleases(ctx context.Context, params api.GetKeyReleasesParams) (api.GetKeyReleasesRes, error) {
	lc := listenerCtxFrom(ctx)
	etag := etagOf("releases", params.Key, params.After.Value, obj.keyFreshness(params.Key))
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	body, err := obj.cachedBytes(ctx, etag, func(buildCtx context.Context) ([]byte, error) {
		listObj, buildErr := dataapi.BuildReleaseList(buildCtx, obj.deps.Storage, obj.deps.State, params.Key, params.After.Value, obj.listPageSize(), obj.linkCtx(lc))
		if buildErr != nil {
			return nil, buildErr
		}
		return encodeJSON(&listObj), nil
	})
	if err != nil {
		return nil, err
	}
	return &api.GetKeyReleasesOKTextPlainHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     api.GetKeyReleasesOKTextPlain{Data: bytes.NewReader(body)},
	}, nil
}

// GetVersionFile dispatches `/{key}/{versionFile}` by extension.
// `.json` caches release detail, archives are streamed from disk, an extension-less path renders HTML.
func (obj *funcObj) GetVersionFile(ctx context.Context, params api.GetVersionFileParams) (api.GetVersionFileRes, error) {
	switch {
	case strings.HasSuffix(params.VersionFile, ".json"):
		return obj.versionDetail(ctx, params, strings.TrimSuffix(params.VersionFile, ".json"))
	case strings.HasSuffix(params.VersionFile, ".tar.gz"):
		return obj.versionArchive(ctx, params, strings.TrimSuffix(params.VersionFile, ".tar.gz"), archive.FormatTarGz)
	case strings.HasSuffix(params.VersionFile, ".zip"):
		return obj.versionArchive(ctx, params, strings.TrimSuffix(params.VersionFile, ".zip"), archive.FormatZip)
	default:
		return obj.versionPage(ctx, params, params.VersionFile)
	}
}

func (obj *funcObj) versionPage(ctx context.Context, params api.GetVersionFileParams, version string) (api.GetVersionFileRes, error) {
	lc := listenerCtxFrom(ctx)
	listenerID := obj.overlayListenerCtx(lc).ListenerID.String()
	etag := etagOf("version-page", params.Key, version, listenerID, lc.scheme(), obj.keyFreshness(params.Key))
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	// Alternate snippets use the full opposite-entry context to avoid mixing host-sensitive artifacts.
	var altOv webui.OverlayInterface
	altListenerID := ""
	if altLC, ok := obj.alternateListenerCtx(lc); ok {
		altOv = obj.overlayWeb(altLC)
		altListenerID = obj.overlayListenerCtx(altLC).ListenerID.String()
	}
	htmlArr, found, err := webui.Version(ctx, obj.deps.State, obj.deps.Storage, obj.overlayWeb(lc), obj.linkCtx(lc), obj.viewContext(lc), params.Key, version, listenerID, altOv, altListenerID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, serr.ErrNotFound
	}
	return &api.GetVersionFileOKTextHTMLHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     api.GetVersionFileOKTextHTML{Data: bytes.NewReader(htmlArr)},
	}, nil
}

func (obj *funcObj) versionDetail(ctx context.Context, params api.GetVersionFileParams, version string) (api.GetVersionFileRes, error) {
	etag := etagOf("detail", params.Key, version, obj.keyFreshness(params.Key))
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	body, found, err := dataapi.BuildReleaseDetail(ctx, obj.deps.Storage, obj.deps.State, params.Key, version, obj.linkCtx(listenerCtxFrom(ctx)))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, serr.ErrNotFound
	}
	return &api.ReleaseDetailObjHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     body,
	}, nil
}

func universalArchiveFormatVersion(format archive.FormatType) (uint32, bool) {
	switch format {
	case archive.FormatZip:
		return overlay.UniversalZipFormatVersion, true
	case archive.FormatTarGz:
		return overlay.UniversalTarGzFormatVersion, true
	default:
		return 0, false
	}
}

func archiveDisposition(fileName string) string {
	return `attachment; filename="` + strings.ReplaceAll(fileName, `"`, "") + `"`
}

func (obj *funcObj) versionArchive(ctx context.Context, params api.GetVersionFileParams, version string, format archive.FormatType) (api.GetVersionFileRes, error) {
	formatVersion, ok := universalArchiveFormatVersion(format)
	if !ok {
		return nil, serr.ErrNotFound
	}
	artObj, keyObj, found, err := artifactio.LocateGlobalFormatKey(ctx, obj.deps.Storage, stcode.MaterializerUniversal.String(), string(format), params.Key, version, formatVersion)
	if err != nil {
		return nil, err
	}
	if !found {
		if _, versionFound, getErr := obj.deps.Storage.GetVersion(ctx, params.Key, version); getErr != nil {
			return nil, getErr
		} else if versionFound {
			return nil, serr.ErrUnavailable
		}
		return nil, serr.ErrNotFound
	}
	gate := gateArtifact(ctx, artObj, params.IfNoneMatch, params.Range, params.IfRange)
	if gate.notModified {
		return &api.NotModifiedRespObj{}, nil
	}
	disposition := archiveDisposition(params.Key + "-" + version + "." + string(format))
	// HEAD responds from Locate metadata; size, ETag, and Accept-Ranges are known without opening the file.
	if gate.headOnly {
		return &api.GetVersionFileOKApplicationOctetStreamHeaders{
			AcceptRanges:       api.NewOptString("bytes"),
			ETag:               api.NewOptString(gate.etag),
			CacheControl:       api.NewOptString(obj.cacheControlData()),
			ContentLength:      api.NewOptInt64(int64(artObj.SizeBytes)),
			ContentDisposition: api.NewOptString(disposition),
			Response:           api.GetVersionFileOKApplicationOctetStream{Data: http.NoBody},
		}, nil
	}
	if !gate.spec.satisfiable {
		return nil, serr.ErrRangeNotSatisfiable
	}
	// keyObj is reused so OpenArtifact does not repeat GetArtifact on full-body responses.
	openObj, err := dataapi.OpenArtifact(ctx, obj.deps.Storage, obj.deps.Overlay, keyObj)
	if err != nil {
		return nil, err
	}
	cacheControl := obj.cacheControlData()
	lastModified := openObj.ModTime.UTC().Format(http.TimeFormat)
	if gate.spec.partial {
		return partialResp(openObj.Body, gate.spec, gate.etag, cacheControl, lastModified, disposition)
	}
	return &api.GetVersionFileOKApplicationOctetStreamHeaders{
		AcceptRanges:       api.NewOptString("bytes"),
		ETag:               api.NewOptString(gate.etag),
		CacheControl:       api.NewOptString(cacheControl),
		LastModified:       api.NewOptString(lastModified),
		ContentLength:      api.NewOptInt64(int64(artObj.SizeBytes)),
		ContentDisposition: api.NewOptString(disposition),
		Response:           api.GetVersionFileOKApplicationOctetStream{Data: openObj.Body},
	}, nil
}
