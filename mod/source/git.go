package source

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	lightweigit "github.com/voluminor/lightweigit-loader"
	"github.com/voluminor/lightweigit-loader/bitbucket"
	"github.com/voluminor/lightweigit-loader/github"
	"github.com/voluminor/lightweigit-loader/gitlab"
	"github.com/voluminor/lightweigit-loader/gogsFamily"

	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

// cMaxReleases caps collected release records to avoid OOM on huge histories.
const cMaxReleases = 100_000

// // // // // // // // // //

func parseProvider(rawURL string) (lightweigit.ProviderInterface, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, permanent(fmt.Errorf("parse source url: %w", err))
	}

	host := strings.ToLower(u.Hostname())
	var (
		provider lightweigit.ProviderInterface
		perr     error
	)
	switch {
	case host == "github.com" || strings.HasSuffix(host, ".github.com"):
		provider, perr = github.Parse(rawURL)
	case host == "gitlab.com" || strings.HasSuffix(host, ".gitlab.com"):
		provider, perr = gitlab.Parse(rawURL)
	case host == "bitbucket.org" || strings.HasSuffix(host, ".bitbucket.org"):
		provider, perr = bitbucket.Parse(rawURL)
	default:
		provider, perr = gogsFamily.Parse(rawURL)
	}
	if perr != nil {
		return nil, permanent(fmt.Errorf("classify git provider for %q: %w", host, perr))
	}
	return provider, nil
}

func classifyLoaderErr(err error) error {
	if errors.Is(err, lightweigit.ErrNotFound) {
		return permanent(err)
	}
	// Since adaptive page splitting in the loader, this only survives when one listing item
	// exceeds the response-body cap; that is a permanent content property.
	if errors.Is(err, lightweigit.ErrResponseTooLarge) {
		return permanent(err)
	}
	return err
}

// IsNotFound reports a provider-level "resource missing" response.
// For Gitea/Forgejo, a disabled releases module looks like this and should trigger tag fallback.
func IsNotFound(err error) bool {
	return errors.Is(err, lightweigit.ErrNotFound)
}

// // // // // // // // // //

// collectStream drains a listing stream up to cMaxReleases. On the cap it cancels and drains the
// channel so the loader goroutine cannot block on send; our context.Canceled means truncation.
func collectStream[T any](ctx context.Context, streamFunc func(context.Context, chan T, int) error, depth uint, mapFunc func(T) (GitReleaseObj, bool)) ([]GitReleaseObj, bool, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	itemCh := make(chan T)
	errCh := make(chan error, 1)
	go func() {
		errCh <- streamFunc(streamCtx, itemCh, int(depth))
		close(itemCh)
	}()

	var out []GitReleaseObj
	truncated := false
	for itemObj := range itemCh {
		relObj, keep := mapFunc(itemObj)
		if !keep {
			continue
		}
		if len(out) >= cMaxReleases {
			truncated = true
			cancel()
			for range itemCh {
			}
			break
		}
		out = append(out, relObj)
	}

	if streamErr := <-errCh; streamErr != nil {
		if !truncated || ctx.Err() != nil || !errors.Is(streamErr, context.Canceled) {
			return nil, false, classifyLoaderErr(streamErr)
		}
	}
	return out, truncated, nil
}

// //

// Releases lists source release versions and drops prereleases by policy.
// depth==0 means full history; the loader channel is closed by our goroutine.
// The second result reports truncation at cMaxReleases, which callers must not treat as authoritative for deletion grace.
func (obj *Obj) Releases(ctx context.Context, sourceURL string, depth uint) ([]GitReleaseObj, bool, error) {
	provider, err := parseProvider(sourceURL)
	if err != nil {
		return nil, false, err
	}
	return collectReleases(ctx, provider, depth)
}

func collectReleases(ctx context.Context, provider lightweigit.ProviderInterface, depth uint) ([]GitReleaseObj, bool, error) {
	return collectStream(ctx, provider.ReleasesStream, depth, func(rel lightweigit.ProviderReleaseInterface) (GitReleaseObj, bool) {
		if rel.IsPrerelease() {
			return GitReleaseObj{}, false
		}
		tagObj := rel.Tag()
		return GitReleaseObj{
			Version:    tagObj.String(),
			BodyMD:     rel.BodyMD(),
			ArchiveURL: tagObj.ZIP().String(),
			Format:     cFormatZip,
		}, true
	})
}

// // // // // // // // // //

// Tags lists source tags as release records for keys whose upstream publishes no releases.
// Tags carry no prerelease flag, so storable-semver tags with a prerelease suffix are dropped
// for parity with the release path; non-semver names pass through as raw versions.
// The second result reports truncation at cMaxReleases, same semantics as Releases.
func (obj *Obj) Tags(ctx context.Context, sourceURL string, depth uint) ([]GitReleaseObj, bool, error) {
	provider, err := parseProvider(sourceURL)
	if err != nil {
		return nil, false, err
	}
	return collectTags(ctx, provider, depth)
}

func collectTags(ctx context.Context, provider lightweigit.ProviderInterface, depth uint) ([]GitReleaseObj, bool, error) {
	return collectStream(ctx, provider.TagsStream, depth, func(tagObj lightweigit.ProviderTagInterface) (GitReleaseObj, bool) {
		name := tagObj.String()
		if util.IsStorableSemverPrerelease(name) {
			return GitReleaseObj{}, false
		}
		return GitReleaseObj{
			Version:    name,
			BodyMD:     "",
			ArchiveURL: tagObj.ZIP().String(),
			Format:     cFormatZip,
		}, true
	})
}

// // // // // // // // // //

// FetchArchive downloads a version archive into DestDir with retry, backoff, and size caps.
// It returns the file path; rescan owns extract, publish, and spool cleanup.
func (obj *Obj) FetchArchive(ctx context.Context, reqObj GitFetchRequestObj) (GitFetchResultObj, error) {
	if reqObj.ArchiveURL == "" {
		return GitFetchResultObj{}, permanent(fmt.Errorf("empty archive url for key=%s version=%s", reqObj.Key, reqObj.Version))
	}
	if parsedURL, perr := url.Parse(reqObj.ArchiveURL); perr != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return GitFetchResultObj{}, permanent(fmt.Errorf("unsupported archive url scheme for key=%s version=%s", reqObj.Key, reqObj.Version))
	}
	format := reqObj.Format
	if format == "" {
		format = cFormatZip
	}
	destPath := spoolPath(reqObj.DestDir, cSourceArchiveName)

	var size uint64
	var stateObj resumeStateObj
	runErr := obj.retry.do(ctx, func(ctx context.Context) error {
		release, err := obj.acquireDownload(ctx)
		if err != nil {
			return err
		}
		defer release()

		n, e := obj.streamToSpool(ctx, reqObj.ArchiveURL, destPath, &stateObj)
		size = n
		return e
	})
	if runErr != nil {
		_ = os.Remove(destPath)
		return GitFetchResultObj{}, obj.mapDownloadErr(reqObj, size, runErr)
	}

	return GitFetchResultObj{ArchivePath: destPath, Format: format, SizeBytes: size}, nil
}

func (obj *Obj) mapDownloadErr(reqObj GitFetchRequestObj, size uint64, cause error) error {
	if errors.Is(cause, errArchiveTooLarge) {
		return stcode.NewErrArchiveLimitExceeded(nil, "archive_size", reqObj.Key, obj.maxArchiveSize, size, reqObj.Version)
	}
	timeoutSeconds := uint32(obj.requestTimeout / 1e9)
	return stcode.NewErrSourceDownloadFailed(cMaxAttempts, cause, reqObj.Key, reqObj.ArchiveURL, timeoutSeconds, reqObj.Version)
}
