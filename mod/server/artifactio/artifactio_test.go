package artifactio

import (
	"context"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

type fakeStoreObj struct {
	artifactObj map[core.ArtifactKeyObj]core.ArtifactObj
}

func (obj *fakeStoreObj) GetArtifact(_ context.Context, keyObj core.ArtifactKeyObj) (core.ArtifactObj, bool, error) {
	artifactObj, ok := obj.artifactObj[keyObj]
	return artifactObj, ok, nil
}

func (obj *fakeStoreObj) EnsureArtifactFile(_ context.Context, _ core.ArtifactKeyObj, _ storage.ArtifactBuilderInterface) (*storage.HotFileObj, error) {
	return nil, nil
}

// // // // // // // // // //

func TestLocateKeyExactListener(t *testing.T) {
	keyObj := core.ArtifactKeyObj{
		MaterializerID: stcode.MaterializerGo.String(),
		ArtifactKind:   "zip",
		ListenerID:     stcode.ListenerWeb.String(),
		Key:            "lib",
		Version:        "v1.0.0",
	}
	storeObj := &fakeStoreObj{artifactObj: map[core.ArtifactKeyObj]core.ArtifactObj{
		keyObj: {MaterializerID: keyObj.MaterializerID, ArtifactKind: keyObj.ArtifactKind, ListenerID: keyObj.ListenerID, Key: keyObj.Key, Version: keyObj.Version},
	}}

	artifactObj, resolvedObj, ok, err := LocateKey(context.Background(), storeObj, keyObj.MaterializerID, keyObj.ArtifactKind, keyObj.Key, keyObj.Version, stcode.ListenerWeb)
	if err != nil || !ok {
		t.Fatalf("LocateKey ok=%v err=%v", ok, err)
	}
	if resolvedObj != keyObj || artifactObj.ListenerID != stcode.ListenerWeb.String() {
		t.Fatalf("resolved=%+v artifact=%+v", resolvedObj, artifactObj)
	}
}

func TestLocateKeyDoesNotFallbackToGlobalGoZip(t *testing.T) {
	globalObj := core.ArtifactKeyObj{
		MaterializerID: stcode.MaterializerGo.String(),
		ArtifactKind:   "zip",
		ListenerID:     stcode.ListenerGlobal.String(),
		Key:            "lib",
		Version:        "v1.0.0",
	}
	storeObj := &fakeStoreObj{artifactObj: map[core.ArtifactKeyObj]core.ArtifactObj{
		globalObj: {MaterializerID: globalObj.MaterializerID, ArtifactKind: globalObj.ArtifactKind, ListenerID: globalObj.ListenerID, Key: globalObj.Key, Version: globalObj.Version},
	}}

	_, _, ok, err := LocateKey(context.Background(), storeObj, globalObj.MaterializerID, globalObj.ArtifactKind, globalObj.Key, globalObj.Version, stcode.ListenerWeb)
	if err != nil {
		t.Fatalf("LocateKey returned error: %v", err)
	}
	if ok {
		t.Fatal("LocateKey must not match global go-zip for a listener-specific request")
	}
}

func TestLocateGlobalFormatKeyStillFindsUniversal(t *testing.T) {
	keyObj := core.ArtifactKeyObj{
		MaterializerID: stcode.MaterializerUniversal.String(),
		ArtifactKind:   "zip",
		ListenerID:     stcode.ListenerGlobal.String(),
		Key:            "lib",
		Version:        "v1.0.0",
	}
	storeObj := &fakeStoreObj{artifactObj: map[core.ArtifactKeyObj]core.ArtifactObj{
		keyObj: {MaterializerID: keyObj.MaterializerID, ArtifactKind: keyObj.ArtifactKind, ListenerID: keyObj.ListenerID, Key: keyObj.Key, Version: keyObj.Version, FormatVersion: 7},
	}}

	artifactObj, resolvedObj, ok, err := LocateGlobalFormatKey(context.Background(), storeObj, keyObj.MaterializerID, keyObj.ArtifactKind, keyObj.Key, keyObj.Version, 7)
	if err != nil || !ok {
		t.Fatalf("LocateGlobalFormatKey ok=%v err=%v", ok, err)
	}
	if resolvedObj != keyObj || artifactObj.FormatVersion != 7 {
		t.Fatalf("resolved=%+v artifact=%+v", resolvedObj, artifactObj)
	}
}
