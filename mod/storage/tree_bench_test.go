package storage

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage/treecodec"
)

// // // // // // // // // //

func benchPath(indexValue int, pathBytes int) string {
	baseText := fmt.Sprintf("dir%04d/file%08d.txt", indexValue%128, indexValue)
	if len(baseText) >= pathBytes {
		return baseText
	}
	paddingText := strings.Repeat("x", pathBytes-len(baseText))
	return fmt.Sprintf("dir%04d/%s/file%08d.txt", indexValue%128, paddingText, indexValue)
}

func benchTreeEntries(countValue int, pathBytes int, orderText string) []core.TreeEntryObj {
	entryArr := make([]core.TreeEntryObj, countValue)
	for i := range entryArr {
		contentText := fmt.Sprintf("content-%d", i)
		entryArr[i] = core.TreeEntryObj{
			Path:      benchPath(i, pathBytes),
			Mode:      cModeFile,
			SizeBytes: uint64(len(contentText)),
			BlobHash:  core.HashBytes([]byte(contentText)),
		}
	}
	switch orderText {
	case "reverse":
		sort.Slice(entryArr, func(i, j int) bool {
			return entryArr[i].Path > entryArr[j].Path
		})
	case "random":
		randomObj := rand.New(rand.NewSource(42))
		randomObj.Shuffle(len(entryArr), func(i, j int) {
			entryArr[i], entryArr[j] = entryArr[j], entryArr[i]
		})
	default:
		sort.Slice(entryArr, func(i, j int) bool {
			return entryArr[i].Path < entryArr[j].Path
		})
	}
	return entryArr
}

func benchTreeData(b *testing.B, countValue int, pathBytes int, orderText string) []byte {
	b.Helper()

	entryArr := benchTreeEntries(countValue, pathBytes, orderText)
	dataArr, _, err := treecodec.Encode(entryArr)
	if err != nil {
		b.Fatalf("canonicalTree returned error: %v", err)
	}
	return dataArr
}

// //

func BenchmarkCanonicalTree(b *testing.B) {
	for _, countValue := range []int{1, 32, 1000, 10000} {
		for _, pathBytes := range []int{12, 80, 240} {
			for _, orderText := range []string{"sorted", "reverse", "random"} {
				entryArr := benchTreeEntries(countValue, pathBytes, orderText)
				b.Run(fmt.Sprintf("entries_%d/path_%d/%s", countValue, pathBytes, orderText), func(b *testing.B) {
					b.ReportAllocs()
					b.SetBytes(int64(countValue))
					for i := 0; i < b.N; i++ {
						if _, _, err := treecodec.Encode(entryArr); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func BenchmarkParseBinaryTree(b *testing.B) {
	for _, countValue := range []int{1, 32, 1000, 10000} {
		for _, pathBytes := range []int{12, 80, 240} {
			dataArr := benchTreeData(b, countValue, pathBytes, "sorted")
			b.Run(fmt.Sprintf("entries_%d/path_%d", countValue, pathBytes), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(dataArr)))
				for i := 0; i < b.N; i++ {
					if _, err := treecodec.Decode(dataArr); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkTreePipeline(b *testing.B) {
	for _, countValue := range []int{32, 1000, 10000} {
		entryArr := benchTreeEntries(countValue, 80, "random")
		b.Run(fmt.Sprintf("entries_%d", countValue), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(countValue))
			for i := 0; i < b.N; i++ {
				dataArr, _, err := treecodec.Encode(entryArr)
				if err != nil {
					b.Fatal(err)
				}
				if _, err = treecodec.Decode(dataArr); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
