package markdown

import (
	"strings"
	"testing"
)

// // // // // // // // // //

func TestSafeHTMLStripsXSS(t *testing.T) {
	out := strings.ToLower(string(SafeHTML(
		"# T\n\n<script>alert(1)</script>\n\n[x](javascript:alert(1)) <img src=x onerror=alert(1)>")))
	for _, bad := range []string{"<script", "javascript:", "onerror"} {
		if strings.Contains(out, bad) {
			t.Fatalf("XSS not sanitized (%q present): %q", bad, out)
		}
	}
}

func TestSafeHTMLRendersMarkdown(t *testing.T) {
	out := string(SafeHTML("# Head\n\n**bold**"))
	if !strings.Contains(out, "<h1>") || !strings.Contains(out, "<strong>bold</strong>") {
		t.Fatalf("markdown not rendered: %q", out)
	}
}

func TestSafeHTMLEmpty(t *testing.T) {
	if SafeHTML("") != nil {
		t.Fatal("empty input must return nil")
	}
}
