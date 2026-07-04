package overlay

import (
	"context"

	"golang.org/x/mod/semver"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

type storageBlobSourceObj struct {
	st StorageInterface
}

// UseBlob reads a blob from storage and synchronously passes it to useFunc without rewrite.
func (s storageBlobSourceObj) UseBlob(ctx context.Context, hashObj core.HashObj, useFunc func([]byte) error) error {
	dataArr, err := s.st.ReadBlob(ctx, hashObj)
	if err != nil {
		return err
	}
	return useFunc(dataArr)
}

// // // // // // // // // //

func universalTopDir(key string, version string) string {
	return key + "-" + version
}

func goModuleTopDir(targetModulePath string, version string) string {
	return targetModulePath + "@" + version
}

func (obj *Obj) targetModulePath(key string, version string, listenerCtxObj ListenerCtxObj) string {
	host := obj.domain
	if listenerCtxObj.EntryHost != "" {
		host = listenerCtxObj.EntryHost
	}
	base := host + "/" + key
	if obj.nested && obj.routingPrefix != "" {
		base = host + "/" + obj.routingPrefix + "/" + key
	}
	if major := semver.Major(version); major != "" && major != "v0" && major != "v1" {
		base += "/" + major
	}
	return base
}
