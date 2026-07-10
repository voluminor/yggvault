package view

import (
	"html/template"
	"strconv"
	"strings"
	"time"
)

// // // // // // // // // //

type (
	// KeyObj is the external input for key.html.
	KeyObj struct {
		Context       ContextObj
		Key           string
		LatestVersion string
		Source        SourceObj
		Overlays      []string
		Install       []CodeSnippetObj
		AltInstall    []CodeSnippetObj
		Versions      []VersionEntryObj
		Page          PageObj
	}

	// PageObj carries the version count and the two keyset pager cursors.
	// An empty cursor hides that direction's link; Newest gates the "released" date line.
	PageObj struct {
		Total       uint64
		Newest      bool
		NewerCursor string
		OlderCursor string
	}

	// VersionEntryObj describes one version row in the key page input.
	VersionEntryObj struct {
		Version         string
		IngestedAt      time.Time
		SourceSizeBytes uint64
		// Go and Composer mark whether this exact version is served in that ecosystem.
		Go       bool
		Composer bool
	}
)

// // // // // // // // // //

type (
	versionRowObj struct {
		Version, URL, Time, Size string
		Go, Composer             bool
	}

	keyTemplateObj struct {
		Head             headObj
		Key, Latest      string
		Count            uint64
		Source           SourceObj
		Overlays         []string
		Install          []CodeSnippetObj
		AltInstall       []CodeSnippetObj
		AltLabel         string
		Versions         []versionRowObj
		PrevURL, NextURL string
	}
)

// // // // // // // // // //

func buildKey(inputObj KeyObj, css template.CSS) keyTemplateObj {
	versionArr := make([]versionRowObj, 0, len(inputObj.Versions))
	for i := range inputObj.Versions {
		versionObj := inputObj.Versions[i]
		versionArr = append(versionArr, versionRowObj{
			Version:  versionObj.Version,
			URL:      versionPath(inputObj.Context, inputObj.Key, versionObj.Version),
			Time:     versionObj.IngestedAt.UTC().Format("2006-01-02"),
			Size:     humanBytes(versionObj.SourceSizeBytes),
			Go:       versionObj.Go,
			Composer: versionObj.Composer,
		})
	}

	baseURL := homePath(inputObj.Context, inputObj.Key)
	prevURL, nextURL := "", ""
	if inputObj.Page.NewerCursor != "" {
		prevURL = baseURL + "?before=" + inputObj.Page.NewerCursor
	}
	if inputObj.Page.OlderCursor != "" {
		nextURL = baseURL + "?after=" + inputObj.Page.OlderCursor
	}

	srcObj := normalizeSource(inputObj.Source)
	desc := "no versions mirrored yet"
	if inputObj.LatestVersion != "" {
		desc = "latest " + inputObj.LatestVersion
		if inputObj.Page.Newest && len(inputObj.Versions) > 0 && !inputObj.Versions[0].IngestedAt.IsZero() {
			desc += " released " + inputObj.Versions[0].IngestedAt.UTC().Format("2006-01-02")
		}
		desc += " · " + strconv.FormatUint(inputObj.Page.Total, 10) + " versions mirrored"
		if len(inputObj.Overlays) > 0 {
			desc += " · " + strings.Join(inputObj.Overlays, ", ") + " overlays"
		}
	}
	desc += " · source " + srcObj.Status
	if srcObj.URL != "" {
		desc += " · upstream " + srcObj.URL
	}
	return keyTemplateObj{
		Head:       head(inputObj.Context, css, inputObj.Key+" - "+inputObj.Context.Service.Name, inputObj.Key+" · "+desc, "og/"+inputObj.Key, homePath(inputObj.Context, inputObj.Key), homePath(inputObj.Context, inputObj.Key)+"/releases.xml"),
		Key:        inputObj.Key,
		Latest:     inputObj.LatestVersion,
		Count:      inputObj.Page.Total,
		Source:     srcObj,
		Overlays:   inputObj.Overlays,
		Install:    inputObj.Install,
		AltInstall: inputObj.AltInstall,
		AltLabel:   altChannelLabel(inputObj.Context.Alternate.Channel),
		Versions:   versionArr,
		PrevURL:    prevURL,
		NextURL:    nextURL,
	}
}
