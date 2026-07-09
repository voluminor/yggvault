package overlay

import (
	"bytes"
	"strings"
	"testing"
)

// // // // // // // // // //

func rewriteBenchCases() []struct {
	name string
	data []byte
	old  []byte
	new  []byte
} {
	oldArr := []byte("example.com/foo")
	newArr := []byte("mirror.example/foo")
	return []struct {
		name string
		data []byte
		old  []byte
		new  []byte
	}{
		{
			name: "small go.mod",
			data: []byte("module example.com/foo\n\nrequire example.com/foo/bar v1.2.3\nreplace example.com/foo => ../foo\n"),
			old:  oldArr,
			new:  newArr,
		},
		{
			name: "typical go file",
			data: []byte(strings.Repeat("package foo\n\nimport \"example.com/foo/bar\"\n\nfunc f() string { return \"example.com/foo\" }\n", 96)),
			old:  oldArr,
			new:  newArr,
		},
		{
			name: "large no match",
			data: bytes.Repeat([]byte("package foo\nconst x = \"no module path here\"\n"), 1<<15),
			old:  oldArr,
			new:  newArr,
		},
		{
			name: "dense boundaries",
			data: []byte(strings.Repeat("example.com/foo example.com/foobar x/example.com/foo example.com/foo/bar\n", 128)),
			old:  oldArr,
			new:  newArr,
		},
	}
}

func TestRewriteSizeMatchesContentLength(t *testing.T) {
	for _, caseObj := range rewriteBenchCases() {
		t.Run(caseObj.name, func(t *testing.T) {
			contentArr := rewriteContent(caseObj.data, caseObj.old, caseObj.new)
			if gotValue := rewrittenSize(caseObj.data, caseObj.old, caseObj.new); gotValue != len(contentArr) {
				t.Fatalf("rewrittenSize=%d len(rewriteContent)=%d", gotValue, len(contentArr))
			}
		})
	}
}

func BenchmarkRewriteContent(b *testing.B) {
	for _, caseObj := range rewriteBenchCases() {
		b.Run(caseObj.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = rewriteContent(caseObj.data, caseObj.old, caseObj.new)
			}
		})
	}
}

func benchmarkRewriteSize(b *testing.B) {
	for _, caseObj := range rewriteBenchCases() {
		b.Run(caseObj.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = rewrittenSize(caseObj.data, caseObj.old, caseObj.new)
			}
		})
	}
}

func BenchmarkRewriteSize(b *testing.B) {
	benchmarkRewriteSize(b)
}

func BenchmarkRewrittenSize(b *testing.B) {
	benchmarkRewriteSize(b)
}
