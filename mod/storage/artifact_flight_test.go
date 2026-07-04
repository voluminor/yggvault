package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

type ctxStallBuilderObj struct{}

type chunkedBuilderObj struct {
	chunks [][]byte
	gap    time.Duration
}

func (ctxStallBuilderObj) Build(ctx context.Context, _ io.Writer) error {
	<-ctx.Done()
	return ctx.Err()
}

func (obj *chunkedBuilderObj) Build(ctx context.Context, writer io.Writer) error {
	for _, chunkArr := range obj.chunks {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(obj.gap):
		}
		if _, err := writer.Write(chunkArr); err != nil {
			return err
		}
	}
	return nil
}

// //

func registerTestArtifact(t *testing.T, obj *Obj, bodyArr []byte) core.ArtifactObj {
	t.Helper()
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
		FormatVersion:  cDefaultFormat,
	}
	if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact returned error: %v", err)
	}
	return artifactObj
}

// // // // // // // // // //

func TestBuildArtifactFlightExistingFileResultIsShared(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Hot.Retain = stcfg.CacheRetainModeAll
	obj := newTestObj(t, configObj)

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	bodyArr := []byte("artifact-body-shared")
	artifactObj := registerTestArtifact(t, obj, bodyArr)
	keyObj := artifactKeyFromObj(artifactObj)
	builderObj := &artifactBuilderObj{dataArr: bodyArr}

	firstObj, err := obj.EnsureArtifactFile(context.Background(), keyObj, builderObj)
	if err != nil {
		t.Fatalf("EnsureArtifactFile returned error: %v", err)
	}
	if err = firstObj.Close(); err != nil {
		t.Fatalf("first Close returned error: %v", err)
	}

	resultObj, err := obj.buildArtifactFlight(context.Background(), keyObj, builderObj)
	if err != nil {
		t.Fatalf("buildArtifactFlight returned error: %v", err)
	}
	if _, ok := resultObj.(*hotSharedFileObj); !ok {
		t.Fatalf("existing-file flight result type = %T, want *hotSharedFileObj", resultObj)
	}

	fileOne, err := artifactResultFile(resultObj)
	if err != nil {
		t.Fatalf("artifactResultFile(one) returned error: %v", err)
	}
	fileTwo, err := artifactResultFile(resultObj)
	if err != nil {
		_ = fileOne.Close()
		t.Fatalf("artifactResultFile(two) returned error: %v", err)
	}
	if fileOne.File == fileTwo.File {
		_ = fileOne.Close()
		_ = fileTwo.Close()
		t.Fatalf("waiters share a single descriptor")
	}

	if err = fileOne.Close(); err != nil {
		_ = fileTwo.Close()
		t.Fatalf("first waiter Close returned error: %v", err)
	}
	gotArr := make([]byte, len(bodyArr))
	if _, err = fileTwo.File.ReadAt(gotArr, 0); err != nil {
		_ = fileTwo.Close()
		t.Fatalf("second descriptor unreadable after first Close: %v", err)
	}
	if !bytes.Equal(gotArr, bodyArr) {
		_ = fileTwo.Close()
		t.Fatalf("second descriptor content mismatch: got %q want %q", gotArr, bodyArr)
	}
	if err = fileTwo.Close(); err != nil {
		t.Fatalf("second waiter Close returned error: %v", err)
	}

	if _, err = artifactResultFile(&HotFileObj{Path: fileTwo.Path}); err == nil {
		t.Fatalf("artifactResultFile must reject a raw *HotFileObj")
	}
}

func TestBuildHotFileStalledBuilderAborts(t *testing.T) {
	prevIdle := cArtifactBuildIdle
	cArtifactBuildIdle = 50 * time.Millisecond
	defer func() { cArtifactBuildIdle = prevIdle }()

	configObj := newTestConfigObj(t)
	obj := newTestObj(t, configObj)
	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	artifactObj := registerTestArtifact(t, obj, []byte("stalled-artifact-body"))

	_, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), ctxStallBuilderObj{})
	if !errors.Is(err, errArtifactBuildStalled) {
		t.Fatalf("stalled build error = %v, want errArtifactBuildStalled", err)
	}
}

func TestBuildHotFileProgressKeepsBuildAlive(t *testing.T) {
	prevIdle := cArtifactBuildIdle
	cArtifactBuildIdle = 60 * time.Millisecond
	defer func() { cArtifactBuildIdle = prevIdle }()

	configObj := newTestConfigObj(t)
	obj := newTestObj(t, configObj)
	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})

	chunkArr := [][]byte{[]byte("aaaa"), []byte("bbbb"), []byte("cccc"), []byte("dddd"), []byte("eeee"), []byte("ffff")}
	var bodyBuf bytes.Buffer
	for _, chunk := range chunkArr {
		bodyBuf.Write(chunk)
	}
	bodyArr := bodyBuf.Bytes()
	artifactObj := registerTestArtifact(t, obj, bodyArr)

	builderObj := &chunkedBuilderObj{chunks: chunkArr, gap: 15 * time.Millisecond}
	fileObj, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), builderObj)
	if err != nil {
		t.Fatalf("progressing build aborted: %v", err)
	}
	gotArr := make([]byte, len(bodyArr))
	if _, err = fileObj.File.ReadAt(gotArr, 0); err != nil {
		_ = fileObj.Close()
		t.Fatalf("built artifact unreadable: %v", err)
	}
	if !bytes.Equal(gotArr, bodyArr) {
		_ = fileObj.Close()
		t.Fatalf("built artifact content mismatch: got %q want %q", gotArr, bodyArr)
	}
	if err = fileObj.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}
