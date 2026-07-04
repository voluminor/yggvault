package server

import (
	"bytes"
	"context"
	"strconv"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/server/serr"
	"github.com/voluminor/yggvault/mod/server/sitemap"
	"github.com/voluminor/yggvault/mod/view"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

const (
	// cAssetCacheControl keeps immutable favicon/logo for a day; the strong ETag makes revalidation cheap.
	cAssetCacheControl = "public, max-age=86400"
	cOgDateLayout      = "2006-01-02"
)

// cLogoSizeArr restricts logo PNG sizes; all other sizes get 404.
var cLogoSizeArr = []int{16, 32, 48, 180, 192, 512}

// //

type staticAssetObj struct {
	body []byte
	etag string
}

// assetSnapshotObj holds favicon and logo PNGs rendered once at startup.
type assetSnapshotObj struct {
	favicon staticAssetObj
	logo    map[int]staticAssetObj
}

func assetETag(bodyArr []byte) string {
	return `"` + core.HashBytes(bodyArr).Hex() + `"`
}

func buildAssetSnapshot() (*assetSnapshotObj, error) {
	faviconArr, err := view.IconICO()
	if err != nil {
		return nil, err
	}
	snapObj := &assetSnapshotObj{
		favicon: staticAssetObj{body: faviconArr, etag: assetETag(faviconArr)},
		logo:    make(map[int]staticAssetObj, len(cLogoSizeArr)),
	}
	for _, edge := range cLogoSizeArr {
		bodyArr, logoErr := view.LogoPNG(edge)
		if logoErr != nil {
			return nil, logoErr
		}
		snapObj.logo[edge] = staticAssetObj{body: bodyArr, etag: assetETag(bodyArr)}
	}
	return snapObj, nil
}

// //

func ogDate(tsObj time.Time) string {
	if tsObj.IsZero() {
		return ""
	}
	return "updated " + tsObj.UTC().Format(cOgDateLayout)
}

// ogBottom builds the banner footer from date and configured canonical domain.
// The domain is node-static, so host-independent banner ETags remain correct.
func (obj *funcObj) ogBottom(tsObj time.Time) string {
	text := ogDate(tsObj)
	if domain := obj.deps.Config.Web.Server.Domain; domain != "" {
		if text != "" {
			text += " - "
		}
		text += domain
	}
	return text
}

// ogOverlays extracts version overlay names for the banner subline.
func ogOverlays(ctx context.Context, store DataStoreInterface, key string, version string) []string {
	detectionObj, ok, err := store.GetDetection(ctx, key, version)
	if err != nil || !ok {
		return nil
	}
	var overlayArr []string
	if detectionObj.IsGo {
		overlayArr = append(overlayArr, "go")
	}
	if detectionObj.IsComposer {
		overlayArr = append(overlayArr, "composer")
	}
	return overlayArr
}

func (obj *funcObj) sitemapSize() int {
	return max(1, int(obj.deps.Config.Web.Pages.SitemapSize))
}

func (obj *funcObj) nodeStats() (moduleCount int, versionTotal uint64, lastPublish time.Time) {
	for _, keyStateObj := range obj.deps.State.KeyStates() {
		if keyStateObj.VersionCount == 0 {
			continue
		}
		moduleCount++
		versionTotal += keyStateObj.VersionCount
		if keyStateObj.LastPublishTS.After(lastPublish) {
			lastPublish = keyStateObj.LastPublishTS
		}
	}
	return moduleCount, versionTotal, lastPublish
}

// // // // // // // // // //

// GetFavicon serves the prebuilt multi-resolution favicon with a strong content ETag.
func (obj *funcObj) GetFavicon(ctx context.Context, params api.GetFaviconParams) (api.GetFaviconRes, error) {
	assetObj := obj.assets.favicon
	if condMatch(params.IfNoneMatch, assetObj.etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	return &api.GetFaviconOKHeaders{
		ETag:         api.NewOptString(assetObj.etag),
		CacheControl: api.NewOptString(cAssetCacheControl),
		Response:     api.GetFaviconOK{Data: bytes.NewReader(assetObj.body)},
	}, nil
}

// GetLogo serves the prebuilt logo PNG only for whitelisted sizes.
func (obj *funcObj) GetLogo(ctx context.Context, params api.GetLogoParams) (api.GetLogoRes, error) {
	edge, err := strconv.Atoi(params.Size)
	if err != nil {
		return nil, serr.ErrNotFound
	}
	assetObj, ok := obj.assets.logo[edge]
	if !ok {
		return nil, serr.ErrNotFound
	}
	if condMatch(params.IfNoneMatch, assetObj.etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	return &api.GetLogoOKHeaders{
		ETag:         api.NewOptString(assetObj.etag),
		CacheControl: api.NewOptString(cAssetCacheControl),
		Response:     api.GetLogoOK{Data: bytes.NewReader(assetObj.body)},
	}, nil
}

// GetSitemap serves the host-sensitive XML sitemap; absolute URLs and freshness feed into the ETag.
func (obj *funcObj) GetSitemap(ctx context.Context, params api.GetSitemapParams) (api.GetSitemapRes, error) {
	lc := listenerCtxFrom(ctx)
	etag := etagOf("sitemap", sitemap.GenVersion, lc.scheme(), lc.entryHost, obj.crossFreshness())
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	body, err := obj.cachedBytes(ctx, etag, func(buildCtx context.Context) ([]byte, error) {
		return sitemap.Build(buildCtx, obj.deps.Storage, obj.deps.State, obj.linkCtx(lc), obj.sitemapSize(), lc.publicMetricsEnabled)
	})
	if err != nil {
		return nil, err
	}
	return &api.GetSitemapOKHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     api.GetSitemapOK{Data: bytes.NewReader(body)},
	}, nil
}

// GetOgNode serves the host-independent node Open Graph banner with counters and the last publish date.
func (obj *funcObj) GetOgNode(ctx context.Context, params api.GetOgNodeParams) (api.GetOgNodeRes, error) {
	etag := etagOf("og-node", view.OGBannerGenVersion, obj.crossFreshness())
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	body, err := obj.cachedBytes(ctx, etag, func(context.Context) ([]byte, error) {
		moduleCount, versionTotal, lastPublish := obj.nodeStats()
		return view.OGNodeBannerPNG(moduleCount, versionTotal, cViewTagline, obj.ogBottom(lastPublish))
	})
	if err != nil {
		return nil, err
	}
	return &api.GetOgNodeOKHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     api.GetOgNodeOK{Data: bytes.NewReader(body)},
	}, nil
}

// GetOgKey serves the key's Open Graph banner: name, latest version, count and last publish.
func (obj *funcObj) GetOgKey(ctx context.Context, params api.GetOgKeyParams) (api.GetOgKeyRes, error) {
	keyStateObj, ok := obj.deps.State.KeyState(params.Key)
	if !ok || keyStateObj.VersionCount == 0 {
		return nil, serr.ErrNotFound
	}
	etag := etagOf("og-key", view.OGBannerGenVersion, params.Key, obj.keyFreshness(params.Key))
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	body, err := obj.cachedBytes(ctx, etag, func(buildCtx context.Context) ([]byte, error) {
		overlayArr := ogOverlays(buildCtx, obj.deps.Storage, params.Key, keyStateObj.LatestVersion)
		return view.OGKeyBannerPNG(params.Key, keyStateObj.LatestVersion, keyStateObj.VersionCount, overlayArr, obj.ogBottom(keyStateObj.LastPublishTS))
	})
	if err != nil {
		return nil, err
	}
	return &api.GetOgKeyOKHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     api.GetOgKeyOK{Data: bytes.NewReader(body)},
	}, nil
}

// GetOgVersion serves the version's Open Graph banner; unknown key/version yield 404.
func (obj *funcObj) GetOgVersion(ctx context.Context, params api.GetOgVersionParams) (api.GetOgVersionRes, error) {
	// keyFreshness changes on version add/delete, so revalidation is safe before the storage probe.
	etag := etagOf("og-version", view.OGBannerGenVersion, params.Key, params.Version, obj.keyFreshness(params.Key))
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	versionObj, ok, err := obj.deps.Storage.GetVersion(ctx, params.Key, params.Version)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, serr.ErrNotFound
	}
	body, err := obj.cachedBytes(ctx, etag, func(buildCtx context.Context) ([]byte, error) {
		overlayArr := ogOverlays(buildCtx, obj.deps.Storage, params.Key, params.Version)
		return view.OGVersionBannerPNG(params.Key, params.Version, versionObj.SourceSizeBytes, overlayArr, obj.ogBottom(versionObj.IngestTS))
	})
	if err != nil {
		return nil, err
	}
	return &api.GetOgVersionOKHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     api.GetOgVersionOK{Data: bytes.NewReader(body)},
	}, nil
}
