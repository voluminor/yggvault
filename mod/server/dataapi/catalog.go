package dataapi

import (
	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

// BuildCatalog maps the state key snapshot into the catalog schema without storage reads.
// Each key URL points to its release list.
func BuildCatalog(st StateReaderInterface, linkObj link.Obj) api.CatalogObj {
	keyStateArr := st.KeyStates()
	itemArr := make([]api.CatalogObjKeysItem, 0, len(keyStateArr))
	for i := range keyStateArr {
		keyStateObj := keyStateArr[i]
		itemObj := api.CatalogObjKeysItem{
			Key:            keyStateObj.Key,
			Source:         sourceStatusOf(keyStateObj),
			Status:         mapKeyStatus(keyStateObj.Status),
			Classification: mapClassification(keyStateObj),
			URL:            api.NewOptString(linkObj.Key(keyStateObj.Key, "/releases.json")),
		}
		if keyStateObj.LatestVersion != "" {
			itemObj.Latest = api.NewOptString(keyStateObj.LatestVersion)
		}
		itemArr = append(itemArr, itemObj)
	}
	return api.CatalogObj{Keys: itemArr}
}
