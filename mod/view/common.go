package view

import (
	"fmt"
	"html/template"
	"net/url"
	"strconv"
	"strings"
)

// // // // // // // // // //

type (
	// ContextObj contains request and service data shared by all page inputs.
	ContextObj struct {
		Service    ServiceObj
		Client     ClientObj
		Alternate  AlternateObj
		Navigation []ActionObj
	}

	// ServiceObj describes the public service identity, extended by the config info card when set.
	ServiceObj struct {
		Name        string
		Tagline     string
		HomeURL     string
		InfoName    string
		Description string
		Location    string
		Contacts    []ContactGroupObj
		// BuildVersion and BuildDate come from target/meta_gen for the footer.
		BuildVersion string
		BuildDate    string
	}

	// ContactGroupObj is one public contact group from the node info card.
	ContactGroupObj struct {
		Name   string
		Values []string
	}

	// ClientObj describes the listener that served the current page.
	ClientObj struct {
		Channel string
		Scheme  string
		Host    string
	}

	// AlternateObj describes the opposite entry channel when both web and ygg are configured.
	// Host is used for links; CopyHost is used for copy-paste commands such as .pk.ygg.
	AlternateObj struct {
		Channel  string
		Scheme   string
		Host     string
		CopyHost string
	}

	// ActionObj describes a route, page action, or alternate entry link.
	ActionObj struct {
		Label  string
		Detail string
		URL    string
		Kind   string
	}

	// SourceObj describes upstream availability and origin metadata.
	SourceObj struct {
		URL            string
		Status         string
		Classification string
		// Label is the rendered badge text; git labels prefer the concrete forge host.
		Label string
	}

	// CodeSnippetObj describes a command or install recipe supplied by the caller.
	CodeSnippetObj struct {
		Label string
		Body  string
	}
)

// // // // // // // // // //

type headObj struct {
	Title        string
	Description  string
	CSS          template.CSS
	Home         string
	Service      string
	InfoName     string
	Location     string
	BuildVersion string
	BuildDate    string
	ChannelClass string
	ChannelLabel string
	AltLabel     string
	AltURL       string
	FeedURL      string
	Navigation   []ActionObj
	FaviconHref  string
	LogoHref     string
	OGImageURL   string
}

// // // // // // // // // //

func nonEmpty(text string, fallback string) string {
	if strings.TrimSpace(text) != "" {
		return text
	}
	return fallback
}

func cleanHome(ctxObj ContextObj) string {
	home := strings.TrimSpace(ctxObj.Service.HomeURL)
	if home == "" {
		return "/"
	}
	if !strings.HasSuffix(home, "/") {
		home += "/"
	}
	return home
}

func homePath(ctxObj ContextObj, rel string) string {
	return cleanHome(ctxObj) + rel
}

func absPath(ctxObj ContextObj, pathText string) string {
	if ctxObj.Client.Host == "" {
		return pathText
	}
	return nonEmpty(ctxObj.Client.Scheme, "http") + "://" + ctxObj.Client.Host + pathText
}

func versionPath(ctxObj ContextObj, key string, version string) string {
	return homePath(ctxObj, key) + "/" + version
}

func humanBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatUint(n, 10) + " B"
	}
	div, exp := uint64(unit), 0
	for nn := n / unit; nn >= unit; nn /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// forgeLabel refines generic git class to a known forge host; unknown hosts render as-is.
func forgeLabel(rawURL string) string {
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsedURL.Hostname())
	switch {
	case host == "github.com" || strings.HasSuffix(host, ".github.com"):
		return "github"
	case host == "gitlab.com" || strings.HasSuffix(host, ".gitlab.com"):
		return "gitlab"
	case host == "bitbucket.org" || strings.HasSuffix(host, ".bitbucket.org"):
		return "bitbucket"
	default:
		return host
	}
}

// normalizeSource defaults missing availability to unknown and derives source badge text.
func normalizeSource(srcObj SourceObj) SourceObj {
	srcObj.Status = nonEmpty(srcObj.Status, "unknown")
	srcObj.Label = srcObj.Classification
	if srcObj.Classification == "git" {
		srcObj.Label = nonEmpty(forgeLabel(srcObj.URL), "git")
	}
	return srcObj
}

// // // // // // // // // //

// channelClass maps channel name to a badge CSS modifier.
func channelClass(channel string) string {
	if channel == "ygg" {
		return "ygg"
	}
	return "web"
}

// channelLabel returns the human-readable current-entry badge label.
func channelLabel(channel string) string {
	if channel == "ygg" {
		return "connected via yggdrasil"
	}
	return "connected via regular web"
}

// altChannelLabel labels collapsed alternate-entry blocks.
func altChannelLabel(channel string) string {
	if channel == "ygg" {
		return "yggdrasil mesh"
	}
	return "regular web"
}

// head builds common page metadata. pagePath targets the same page on an alternate entry;
// feedPath is the optional Atom feed path.
func head(ctxObj ContextObj, css template.CSS, title string, desc string, ogRel string, pagePath string, feedPath string) headObj {
	altLabel, altURL := "", ""
	if ctxObj.Alternate.Host != "" {
		altLabel = altChannelLabel(ctxObj.Alternate.Channel)
		altURL = ctxObj.Alternate.Scheme + "://" + ctxObj.Alternate.Host + nonEmpty(pagePath, cleanHome(ctxObj))
	}
	return headObj{
		Title:        title,
		Description:  nonEmpty(desc, nonEmpty(ctxObj.Service.Description, ctxObj.Service.Tagline)),
		CSS:          css,
		Home:         cleanHome(ctxObj),
		Service:      ctxObj.Service.Name,
		InfoName:     ctxObj.Service.InfoName,
		Location:     ctxObj.Service.Location,
		BuildVersion: ctxObj.Service.BuildVersion,
		BuildDate:    ctxObj.Service.BuildDate,
		ChannelClass: channelClass(ctxObj.Client.Channel),
		ChannelLabel: channelLabel(ctxObj.Client.Channel),
		AltLabel:     altLabel,
		AltURL:       altURL,
		FeedURL:      feedPath,
		Navigation:   ctxObj.Navigation,
		FaviconHref:  homePath(ctxObj, "favicon.ico"),
		LogoHref:     homePath(ctxObj, "logo/180"),
		OGImageURL:   absPath(ctxObj, homePath(ctxObj, ogRel)),
	}
}
