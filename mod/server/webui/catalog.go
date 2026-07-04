package webui

import (
	"context"
	"sort"

	"github.com/voluminor/yggvault/mod/view"
)

// // // // // // // // // //

// Catalog builds the catalog page with all keys sorted by name.
// store provides per-key latest detection for badges, cached with the page by ETag.
// ctx comes from the HTTP request so client cancellation stops detection queries.
func Catalog(ctx context.Context, st StateReaderInterface, store VersionReaderInterface, ctxObj view.ContextObj) ([]byte, error) {
	rnd, err := renderer()
	if err != nil {
		return nil, err
	}

	keyStateArr := st.KeyStates()
	sort.Slice(keyStateArr, func(i, j int) bool { return keyStateArr[i].Key < keyStateArr[j].Key })

	itemArr := make([]view.CatalogEntryObj, 0, len(keyStateArr))
	for i := range keyStateArr {
		keyStateObj := keyStateArr[i]
		goFlag, composerFlag := false, false
		if keyStateObj.LatestVersion != "" {
			goFlag, composerFlag = versionEcosystems(ctx, store, keyStateObj.Key, keyStateObj.LatestVersion)
		}
		itemArr = append(itemArr, view.CatalogEntryObj{
			Key:           keyStateObj.Key,
			LatestVersion: keyStateObj.LatestVersion,
			Source:        originOf(keyStateObj),
			Go:            goFlag,
			Composer:      composerFlag,
		})
	}

	return rnd.Catalog(view.CatalogObj{
		Context: ctxObj,
		Keys:    itemArr,
	})
}
