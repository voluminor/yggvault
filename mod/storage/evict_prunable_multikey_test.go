package storage

import (
	"context"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func TestEvictPrunableMultiKey(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Quota.MaxVersionsPerKey = 2
	configObj.Storage.Quota.RetainLatestPerKey = 1
	obj := newTestObj(t, configObj)
	ctx := context.Background()

	keyArr := []string{"key-a", "key-b", "key-c"}
	versionArr := []string{"v1.0.0", "v1.1.0", "v1.2.0"}
	for _, key := range keyArr {
		for _, version := range versionArr {
			if _, err := obj.Publish(ctx, core.PublishObj{
				Key:             key,
				Version:         version,
				SourceHash:      core.HashBytes([]byte(key + version)),
				SourceSizeBytes: 1,
				Entries:         []core.InputEntryObj{{Path: "f.txt", Content: []byte(key + version)}},
			}); err != nil {
				t.Fatalf("publish %s@%s: %v", key, version, err)
			}
		}
	}

	if err := obj.EvictVersions(ctx); err != nil {
		t.Fatalf("EvictVersions: %v", err)
	}

	for _, key := range keyArr {
		gotArr, err := obj.ListVersions(ctx, key, true)
		if err != nil {
			t.Fatalf("ListVersions %s: %v", key, err)
		}
		if len(gotArr) != 2 || gotArr[0].Version != "v1.2.0" || gotArr[1].Version != "v1.1.0" {
			t.Fatalf("key %s after prune: %#v (want [v1.2.0 v1.1.0])", key, gotArr)
		}
	}
}
