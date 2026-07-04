package rescan

import "testing"

// // // // // // // // // //

func TestComposerKeyForName(t *testing.T) {
	obj := &Obj{composerKeyByName: map[string]string{"vendor/pkg": "key-a"}}
	if keyText, ok := obj.ComposerKeyForName("vendor/pkg"); !ok || keyText != "key-a" {
		t.Fatalf("known name: key=%q ok=%v", keyText, ok)
	}
	if _, ok := obj.ComposerKeyForName("vendor/unknown"); ok {
		t.Fatal("unknown name resolved")
	}
	empty := &Obj{}
	if _, ok := empty.ComposerKeyForName("any"); ok {
		t.Fatal("nil map resolved")
	}
}
