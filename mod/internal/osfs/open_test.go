package osfs

import (
	"os"
	"path/filepath"
	"testing"
)

// // // // // // // // // //

// OpenNoFollowWrite must reject a final symlink component and never write through it.
// The contract is cross-platform: unix uses O_NOFOLLOW, non-unix uses an Lstat guard.
func TestOpenNoFollowWriteRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unsupported here: %v", err)
	}
	fileObj, err := OpenNoFollowWrite(link, 0o600)
	if err == nil {
		_ = fileObj.Close()
		t.Fatal("OpenNoFollowWrite must refuse a symlink final component")
	}
	if dataArr, _ := os.ReadFile(target); string(dataArr) != "x" {
		t.Fatalf("target was written through the symlink: %q", dataArr)
	}
}

// Regular path: creating or overwriting a regular file still works.
func TestOpenNoFollowWriteRegular(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	fileObj, err := OpenNoFollowWrite(path, 0o600)
	if err != nil {
		t.Fatalf("OpenNoFollowWrite regular: %v", err)
	}
	if _, err := fileObj.WriteString("hello"); err != nil {
		t.Fatal(err)
	}
	_ = fileObj.Close()
	if dataArr, err := os.ReadFile(path); err != nil || string(dataArr) != "hello" {
		t.Fatalf("regular write readback: %q err=%v", dataArr, err)
	}
}
