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
		AltDownloads         []ArtifactEntryObj
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
		AbsURL    string
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
		AbsURL, SHA256        string
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
		AltDownloads                           []downloadObj
		GoDownloads                            []downloadObj
		Snippets                               []CodeSnippetObj
		AltSnippets                            []CodeSnippetObj
		AltLabel                               string
		NotesHTML                              template.HTML
		NewerURL, NewerVer, OlderURL, OlderVer string
	}
)

// // // // // // // // // //

var cTagRe = regexp.MustCompile(`<[^>]*>`)

func upstreamLabel(deleted bool) string {
	if deleted {
		return "deleted upstream"
	}
	return "present"
}

func notesExcerpt(notesHTML []byte, maxRunes int) string {
	if len(notesHTML) == 0 {
		return ""
	}
	text := html.UnescapeString(cTagRe.ReplaceAllString(string(notesHTML), " "))
	text = strings.Join(strings.Fields(text), " ")
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

func fmtTimestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02 15:04 UTC")
}

func fmtISO(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func buildDownloadRows(ctxObj ContextObj, entryArr []ArtifactEntryObj) ([]downloadObj, []downloadObj) {
	var downloadArr, goDownloadArr []downloadObj
	for i := range entryArr {
		artifactObj := entryArr[i]
		hashArr := make([]hashRowObj, 0, len(artifactObj.Hashes))
		sha256Text := ""
		for _, hashObj := range artifactObj.Hashes {
			if len(hashObj.Sum) == 0 {
				continue
			}
			hexText := hex.EncodeToString(hashObj.Sum)
			hashArr = append(hashArr, hashRowObj{Algo: hashObj.Algo, Hex: hexText})
			if hashObj.Algo == "sha256" {
				sha256Text = hexText
			}
		}
		rowObj := downloadObj{
			Name:   artifactObj.Name,
			Kind:   artifactObj.Kind,
			Size:   humanBytes(artifactObj.SizeBytes),
			URL:    artifactObj.URL,
			AbsURL: nonEmpty(artifactObj.AbsURL, absPath(ctxObj, artifactObj.URL)),
			SHA256: sha256Text,
			Hashes: hashArr,
		}
		if artifactObj.GoModule {
			goDownloadArr = append(goDownloadArr, rowObj)
		} else {
			downloadArr = append(downloadArr, rowObj)
		}
	}
	return downloadArr, goDownloadArr
}

func buildVersion(inputObj VersionObj, css template.CSS) versionTemplateObj {
	downloadArr, goDownloadArr := buildDownloadRows(inputObj.Context, inputObj.Downloads)
	altDownloadArr, _ := buildDownloadRows(inputObj.Context, inputObj.AltDownloads)
	newerURL, olderURL := "", ""
	if inputObj.History.NewerVersion != "" {
		newerURL = versionPath(inputObj.Context, inputObj.Key, inputObj.History.NewerVersion)
	}
	if inputObj.History.OlderVersion != "" {
		olderURL = versionPath(inputObj.Context, inputObj.Key, inputObj.History.OlderVersion)
	}

	srcObj := normalizeSource(inputObj.Source)
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
		Head:         head(inputObj.Context, css, inputObj.Key+"@"+inputObj.Version+" - "+inputObj.Context.Service.Name, desc, "og/"+inputObj.Key+"/"+inputObj.Version, versionPath(inputObj.Context, inputObj.Key, inputObj.Version), homePath(inputObj.Context, inputObj.Key)+"/releases.xml"),
		Key:          inputObj.Key,
		KeyURL:       homePath(inputObj.Context, inputObj.Key),
		Version:      inputObj.Version,
		Size:         humanBytes(inputObj.SourceSizeBytes),
		Ingested:     fmtTimestamp(inputObj.IngestedAt),
		Verified:     fmtTimestamp(inputObj.VerifiedAt),
		IngestedISO:  fmtISO(inputObj.IngestedAt),
		VerifiedISO:  fmtISO(inputObj.VerifiedAt),
		Detected:     inputObj.Detected,
		Overlays:     inputObj.Overlays,
		Source:       srcObj,
		Degraded:     inputObj.Degraded,
		Downloads:    downloadArr,
		AltDownloads: altDownloadArr,
		GoDownloads:  goDownloadArr,
		Snippets:     inputObj.Install,
		AltSnippets:  inputObj.AltInstall,
		AltLabel:     altChannelLabel(inputObj.Context.Alternate.Channel),
		NotesHTML:    template.HTML(notesHTML),
		NewerURL:     newerURL,
		NewerVer:     inputObj.History.NewerVersion,
		OlderURL:     olderURL,
		OlderVer:     inputObj.History.OlderVersion,
	}
}
