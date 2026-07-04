package source

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

const (
	cPublicMirrorJSONMaxBytes = 8 << 20

	cPublicMirrorTreeZip   = "tree-zip"
	cPublicMirrorTreeTarGz = "tree-targz"
)

// // // // // // // // // //

type publicMirrorReleaseListObj struct {
	Releases []publicMirrorReleaseSummaryObj `json:"releases"`
	Next     string                          `json:"next"`
}

type publicMirrorReleaseSummaryObj struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

type publicMirrorReleaseDetailObj struct {
	Version       string                    `json:"version"`
	Hash          string                    `json:"hash"`
	NotesMarkdown string                    `json:"notes_markdown"`
	Artifacts     []publicMirrorArtifactObj `json:"artifacts"`
}

type publicMirrorArtifactObj struct {
	Kind string `json:"kind"`
	URL  string `json:"url"`
}

// // // // // // // // // //

func publicMirrorKeyBase(rootURL string, remoteKey string) (*url.URL, error) {
	u, err := url.Parse(rootURL)
	if err != nil {
		return nil, err
	}
	if u.Host == "" {
		return nil, fmt.Errorf("public mirror url %q has no host", rootURL)
	}
	keyPath := strings.TrimRight(u.Path, "/")
	if keyPath == "" {
		keyPath = "/" + strings.Trim(remoteKey, "/")
	}
	if keyPath == "" || keyPath == "/" {
		return nil, fmt.Errorf("public mirror url %q has no key path", rootURL)
	}
	u.Path = keyPath
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}

func publicMirrorPath(baseObj *url.URL, suffix string) string {
	u := *baseObj
	u.Path = strings.TrimRight(baseObj.Path, "/") + suffix
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func resolvePublicMirrorURL(baseURL string, refText string) (string, error) {
	baseObj, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	refObj, err := url.Parse(refText)
	if err != nil {
		return "", err
	}
	return baseObj.ResolveReference(refObj).String(), nil
}

func parsePublicMirrorHash(text string) (core.HashObj, error) {
	rawArr, err := hex.DecodeString(text)
	if err != nil {
		return core.HashObj{}, err
	}
	return core.HashFromBytes(rawArr)
}

func choosePublicMirrorArtifact(detailObj publicMirrorReleaseDetailObj) (publicMirrorArtifactObj, string, bool) {
	for _, artifactObj := range detailObj.Artifacts {
		if artifactObj.Kind == cPublicMirrorTreeTarGz {
			return artifactObj, archive.FormatTarGz.String(), true
		}
	}
	for _, artifactObj := range detailObj.Artifacts {
		if artifactObj.Kind == cPublicMirrorTreeZip {
			return artifactObj, archive.FormatZip.String(), true
		}
	}
	return publicMirrorArtifactObj{}, "", false
}

func (obj *Obj) getPublicMirrorJSON(ctx context.Context, rawURL string, outObj any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return permanent(err)
	}
	req.Header.Set("User-Agent", cUserAgent)

	resp, err := obj.metaClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		statusErr := fmt.Errorf("public mirror %q: http status %d", rawURL, resp.StatusCode)
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return permanent(statusErr)
		}
		return statusErr
	}

	bodyArr, err := io.ReadAll(io.LimitReader(resp.Body, cPublicMirrorJSONMaxBytes+1))
	if err != nil {
		return err
	}
	if len(bodyArr) > cPublicMirrorJSONMaxBytes {
		return permanent(fmt.Errorf("public mirror %q: json body exceeds %d bytes", rawURL, cPublicMirrorJSONMaxBytes))
	}
	if err := json.Unmarshal(bodyArr, outObj); err != nil {
		return permanent(fmt.Errorf("public mirror %q: decode json: %w", rawURL, err))
	}
	return nil
}

func (obj *Obj) publicMirrorDetail(ctx context.Context, listURL string, summaryObj publicMirrorReleaseSummaryObj, fallbackURL string) (PublicMirrorVersionObj, error) {
	detailURL := fallbackURL
	if summaryObj.URL != "" {
		resolvedURL, err := resolvePublicMirrorURL(listURL, summaryObj.URL)
		if err != nil {
			return PublicMirrorVersionObj{}, permanent(err)
		}
		detailURL = resolvedURL
	}

	var detailObj publicMirrorReleaseDetailObj
	err := obj.retry.do(ctx, func(ctx context.Context) error {
		return obj.getPublicMirrorJSON(ctx, detailURL, &detailObj)
	})
	if err != nil {
		return PublicMirrorVersionObj{}, err
	}
	versionText := detailObj.Version
	if versionText == "" {
		versionText = summaryObj.Version
	}
	if versionText == "" {
		return PublicMirrorVersionObj{}, permanent(fmt.Errorf("public mirror detail %q has empty version", detailURL))
	}
	treeHashObj, err := parsePublicMirrorHash(detailObj.Hash)
	if err != nil {
		return PublicMirrorVersionObj{}, permanent(fmt.Errorf("public mirror detail %q has invalid tree hash: %w", detailURL, err))
	}
	artifactObj, formatText, ok := choosePublicMirrorArtifact(detailObj)
	if !ok {
		return PublicMirrorVersionObj{}, permanent(fmt.Errorf("public mirror detail %q has no universal archive artifact", detailURL))
	}
	archiveURL, err := resolvePublicMirrorURL(detailURL, artifactObj.URL)
	if err != nil {
		return PublicMirrorVersionObj{}, permanent(err)
	}

	return PublicMirrorVersionObj{
		Version:      versionText,
		ReleaseNotes: detailObj.NotesMarkdown,
		TreeHash:     treeHashObj,
		ArchiveURL:   archiveURL,
		Format:       formatText,
	}, nil
}

// PublicMirrorVersions reads a yggvault public API release list and resolves each version's universal archive.
func (obj *Obj) PublicMirrorVersions(ctx context.Context, rootURL string, remoteKey string) ([]PublicMirrorVersionObj, bool, error) {
	baseObj, err := publicMirrorKeyBase(rootURL, remoteKey)
	if err != nil {
		return nil, false, permanent(err)
	}
	listURL := publicMirrorPath(baseObj, "/releases.json")

	outArr := make([]PublicMirrorVersionObj, 0)
	nextText := ""
	truncatedFlag := false
	seenCursorSet := make(map[string]struct{})
	for {
		pageURL := listURL
		if nextText != "" {
			u, err := url.Parse(listURL)
			if err != nil {
				return nil, false, permanent(err)
			}
			q := u.Query()
			q.Set("after", nextText)
			u.RawQuery = q.Encode()
			pageURL = u.String()
		}

		var listObj publicMirrorReleaseListObj
		err := obj.retry.do(ctx, func(ctx context.Context) error {
			return obj.getPublicMirrorJSON(ctx, pageURL, &listObj)
		})
		if err != nil {
			return nil, false, err
		}
		for _, summaryObj := range listObj.Releases {
			if len(outArr) >= cMaxReleases {
				truncatedFlag = true
				return outArr, truncatedFlag, nil
			}
			fallbackURL := publicMirrorPath(baseObj, "/"+url.PathEscape(summaryObj.Version)+".json")
			versionObj, err := obj.publicMirrorDetail(ctx, pageURL, summaryObj, fallbackURL)
			if err != nil {
				return nil, false, err
			}
			outArr = append(outArr, versionObj)
		}
		if listObj.Next == "" {
			return outArr, truncatedFlag, nil
		}
		if len(listObj.Releases) == 0 {
			return nil, false, permanent(fmt.Errorf("public mirror %q returned an empty page with next cursor %q", pageURL, listObj.Next))
		}
		if _, ok := seenCursorSet[listObj.Next]; ok {
			return nil, false, permanent(fmt.Errorf("public mirror %q repeated next cursor %q", listURL, listObj.Next))
		}
		seenCursorSet[listObj.Next] = struct{}{}
		nextText = listObj.Next
	}
}
