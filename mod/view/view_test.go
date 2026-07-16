package view

import (
	"html"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/markdown"
)

// // // // // // // // // //

func testCtx() ContextObj {
	return ContextObj{
		Service: ServiceObj{Name: "yggvault", Tagline: "content-addressed release vault", HomeURL: "/"},
		Client:  ClientObj{Channel: "web", Scheme: "https", Host: "vault.test"},
	}
}

// // // // // // // // // //

// Guard against broken {{...}} inside <style>/<script>: CSS and JS must actually inject.
func TestRenderInjectsCSSAndLiveJS(t *testing.T) {
	rnd, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	catalogArr, err := rnd.Catalog(CatalogObj{Context: testCtx()})
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	catalogText := string(catalogArr)
	if !strings.Contains(catalogText, "--rune:") {
		t.Error("catalog page must embed the stylesheet inside <style>")
	}
	if strings.Contains(catalogText, "{{") || strings.Contains(catalogText, "{ {") {
		t.Error("catalog page must not leak template action braces")
	}

	metricsArr, err := rnd.MetricsIndex(MetricsObj{
		Context:         testCtx(),
		Endpoints:       []ActionObj{{Label: "core", URL: "/metrics/core", Kind: "json"}},
		RefreshInterval: time.Second,
	})
	if err != nil {
		t.Fatalf("MetricsIndex: %v", err)
	}
	metricsText := string(metricsArr)
	if !regexp.MustCompile(`var refreshMS =\s*10000\s*;`).MatchString(metricsText) {
		t.Error("metrics page must clamp the live refresh interval to 10s minimum")
	}
	if strings.Contains(metricsText, "{{") || strings.Contains(metricsText, "{ {") {
		t.Error("metrics page must not leak template action braces")
	}
}

// // // // // // // // // //

// Source badge: git class is refined to forge by host; brother and empty class stay unchanged.
func TestSourceLabels(t *testing.T) {
	cases := map[string]struct {
		srcObj SourceObj
		want   string
	}{
		"github":           {SourceObj{URL: "https://github.com/acme/alpha", Classification: "git"}, "github"},
		"github subdomain": {SourceObj{URL: "https://api.github.com/acme/alpha", Classification: "git"}, "github"},
		"gitlab":           {SourceObj{URL: "https://gitlab.com/acme/alpha", Classification: "git"}, "gitlab"},
		"bitbucket":        {SourceObj{URL: "https://bitbucket.org/acme/alpha", Classification: "git"}, "bitbucket"},
		"other forge":      {SourceObj{URL: "https://codeberg.org/acme/alpha", Classification: "git"}, "codeberg.org"},
		"host with port":   {SourceObj{URL: "https://git.corp.test:8443/acme/alpha", Classification: "git"}, "git.corp.test"},
		"unparsable url":   {SourceObj{URL: "://broken", Classification: "git"}, "git"},
		"empty url":        {SourceObj{URL: "", Classification: "git"}, "git"},
		"brother":          {SourceObj{URL: "https://github.com/acme/alpha", Classification: "brother"}, "brother"},
		"unclassified":     {SourceObj{URL: "https://github.com/acme/alpha", Classification: ""}, ""},
	}
	for name, tc := range cases {
		if got := normalizeSource(tc.srcObj).Label; got != tc.want {
			t.Errorf("%s: label = %q, want %q", name, got, tc.want)
		}
	}
}

// // // // // // // // // //

func excerptReference(markdownText string, maxRunes int) string {
	if strings.TrimSpace(markdownText) == "" {
		return ""
	}
	text := html.UnescapeString(cTagRe.ReplaceAllString(string(markdown.SafeHTML(markdownText)), " "))
	text = strings.Join(strings.Fields(text), " ")
	runeArr := []rune(text)
	if len(runeArr) > maxRunes {
		text = strings.TrimSpace(string(runeArr[:maxRunes-1])) + "…"
	}
	return text
}

func TestNotesExcerpt(t *testing.T) {
	const short = "# Title\n\nHello **world** &amp; friends\n\n- a\n- b"
	if got := notesExcerpt(markdown.SafeHTML(short), 200); got != "Title Hello world & friends a b" {
		t.Errorf("short excerpt = %q, want tags stripped, entities unescaped, whitespace collapsed", got)
	}

	cases := map[string]string{
		"empty":           "",
		"whitespace only": "  \n\t ",
		"short":           short,
		"exact limit":     strings.Repeat("é", 200),
		"one over limit":  strings.Repeat("é", 201),
		"long multibyte":  strings.Repeat("données ", 60),
		"long ascii":      strings.Repeat("lorem ipsum ", 60),
		"cut at space":    strings.Repeat("a ", 150),
	}
	for name, markdownText := range cases {
		want := excerptReference(markdownText, 200)
		if got := notesExcerpt(markdown.SafeHTML(markdownText), 200); got != want {
			t.Errorf("%s: excerpt = %q, want %q", name, got, want)
		}
	}
}
