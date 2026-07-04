package view

import (
	"encoding/hex"
	"html"
	"html/template"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/voluminor/yggvault/mod/markdown"
)

// // // // // // // // // //

type (
	// VersionObj is the external input for version.html.
	VersionObj struct {
		Context              ContextObj
		Key                  string
		Version              string
		SourceSizeBytes      uint64
		UpstreamDeleted      bool
		IngestedAt           time.Time
		VerifiedAt           time.Time
		Detected             []string
		Source               SourceObj
		Overlays             []string
		Degraded             []string
		Downloads            []ArtifactEntryObj
		Install              []CodeSnippetObj
		AltInstall           []CodeSnippetObj
		ReleaseNotesMarkdown string
		History              VersionHistoryObj
	}

	// ArtifactEntryObj describes one downloadable artifact in the version page input.
	// Kind is the plain archive format; Go module archives are separated by GoModule.
	ArtifactEntryObj struct {
		Name      string
		Kind      string
		SizeBytes uint64
		Hashes    []ArtifactHashObj
		URL       string
		GoModule  bool
	}

	// ArtifactHashObj is one stored digest of an artifact body.
	ArtifactHashObj struct {
		Algo string
		Sum  []byte
	}

	// VersionHistoryObj describes neighboring versions for the version page.
	VersionHistoryObj struct {
		NewerVersion string
		OlderVersion string
	}
)

// // // // // // // // // //

type (
	hashRowObj struct {
		Algo, Hex string
	}

	downloadObj struct {
		Name, Kind, Size, URL string
		Hashes                []hashRowObj
	}

	versionTemplateObj struct {
		Head                                   headObj
		Key, KeyURL, Version                   string
		Size                                   string
		Ingested, Verified                     string
		IngestedISO, VerifiedISO               string
		Detected                               []string
		Overlays                               []string
		Source                                 SourceObj
		Degraded                               []string
		Downloads                              []downloadObj
		GoDownloads                            []downloadObj
		Snippets                               []CodeSnippetObj
		AltSnippets                            []CodeSnippetObj
		AltLabel                               string
		NotesHTML                              template.HTML
		NewerURL, NewerVer, OlderURL, OlderVer string
	}
)

// // // // // // // // // //

// cTagRe strips tags from already sanitized HTML for a plain-text excerpt.
var cTagRe = regexp.MustCompile(`<[^>]*>`)

func upstreamLabel(deleted bool) string {
	if deleted {
		return "deleted upstream"
	}
	return "present"
}

// notesExcerpt builds a short plain-text excerpt from rendered release notes for og:description.
func notesExcerpt(notesHTML []byte, maxRunes int) string {
	if len(notesHTML) == 0 {
		return ""
	}
	text := html.UnescapeString(cTagRe.ReplaceAllString(string(notesHTML), " "))
	text = strings.Join(strings.Fields(text), " ")
	// Count runes incrementally to avoid allocating a full []rune for long notes.
	runeCount, cutOffset := 0, 0
	for byteIdx := range text {
		if runeCount == maxRunes-1 {
			cutOffset = byteIdx
		}
		runeCount++
		if runeCount > maxRunes {
			return strings.TrimSpace(text[:cutOffset]) + "…"
		}
	}
	return text
}

// // // // // // // // // //

// fmtTimestamp formats UTC time; zero time means no data and renders empty.
func fmtTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

// fmtISO returns an RFC3339 timestamp for <time datetime>; zero time renders empty.
func fmtISO(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// buildVersion derives template-only fields for version.html.
func buildVersion(inputObj VersionObj, css template.CSS) versionTemplateObj {
	var downloadArr, goDownloadArr []downloadObj
	for i := range inputObj.Downloads {
		artifactObj := inputObj.Downloads[i]
		hashArr := make([]hashRowObj, 0, len(artifactObj.Hashes))
		for _, hashObj := range artifactObj.Hashes {
			if len(hashObj.Sum) == 0 {
				continue
			}
			hashArr = append(hashArr, hashRowObj{Algo: hashObj.Algo, Hex: hex.EncodeToString(hashObj.Sum)})
		}
		rowObj := downloadObj{
			Name:   artifactObj.Name,
			Kind:   artifactObj.Kind,
			Size:   humanBytes(artifactObj.SizeBytes),
			Hashes: hashArr,
			URL:    artifactObj.URL,
		}
		if artifactObj.GoModule {
			goDownloadArr = append(goDownloadArr, rowObj)
		} else {
			downloadArr = append(downloadArr, rowObj)
		}
	}

	newerURL, olderURL := "", ""
	if inputObj.History.NewerVersion != "" {
		newerURL = versionPath(inputObj.Context, inputObj.Key, inputObj.History.NewerVersion)
	}
	if inputObj.History.OlderVersion != "" {
		olderURL = versionPath(inputObj.Context, inputObj.Key, inputObj.History.OlderVersion)
	}

	srcObj := normalizeSource(inputObj.Source)
	// Render notes once; NotesHTML and the OG excerpt share the same sanitized result.
	notesHTML := markdown.SafeHTML(inputObj.ReleaseNotesMarkdown)

	descParts := []string{
		inputObj.Key + " " + inputObj.Version,
		humanBytes(inputObj.SourceSizeBytes) + " source archive",
		"upstream " + upstreamLabel(inputObj.UpstreamDeleted),
		"source " + srcObj.Status,
	}
	if len(inputObj.Downloads) > 0 {
		descParts = append(descParts, strconv.Itoa(len(inputObj.Downloads))+" downloadable artifacts")
	}
	if len(inputObj.Overlays) > 0 {
		descParts = append(descParts, strings.Join(inputObj.Overlays, ", ")+" overlays")
	}
	desc := strings.Join(descParts, " · ")
	if excerpt := notesExcerpt(notesHTML, 200); excerpt != "" {
		desc += " — " + excerpt
	}
	return versionTemplateObj{
		Head:        head(inputObj.Context, css, inputObj.Key+"@"+inputObj.Version+" - "+inputObj.Context.Service.Name, desc, "og/"+inputObj.Key+"/"+inputObj.Version, versionPath(inputObj.Context, inputObj.Key, inputObj.Version), homePath(inputObj.Context, inputObj.Key)+"/releases.xml"),
		Key:         inputObj.Key,
		KeyURL:      homePath(inputObj.Context, inputObj.Key),
		Version:     inputObj.Version,
		Size:        humanBytes(inputObj.SourceSizeBytes),
		Ingested:    fmtTimestamp(inputObj.IngestedAt),
		Verified:    fmtTimestamp(inputObj.VerifiedAt),
		IngestedISO: fmtISO(inputObj.IngestedAt),
		VerifiedISO: fmtISO(inputObj.VerifiedAt),
		Detected:    inputObj.Detected,
		Overlays:    inputObj.Overlays,
		Source:      srcObj,
		Degraded:    inputObj.Degraded,
		Downloads:   downloadArr,
		GoDownloads: goDownloadArr,
		Snippets:    inputObj.Install,
		AltSnippets: inputObj.AltInstall,
		AltLabel:    altChannelLabel(inputObj.Context.Alternate.Channel),
		NotesHTML:   template.HTML(notesHTML),
		NewerURL:    newerURL,
		NewerVer:    inputObj.History.NewerVersion,
		OlderURL:    olderURL,
		OlderVer:    inputObj.History.OlderVersion,
	}
}
