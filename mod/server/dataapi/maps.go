package dataapi

import (
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/target/api"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

func mapKeyStatus(statusObj stcode.OperationalStatusType) api.CatalogObjKeysItemStatus {
	switch statusObj {
	case stcode.OperationalStatusOk:
		return api.CatalogObjKeysItemStatusOk
	case stcode.OperationalStatusDegraded:
		return api.CatalogObjKeysItemStatusDegraded
	default:
		return api.CatalogObjKeysItemStatusError
	}
}

func mapAvailability(availObj stcode.AvailabilityStatusType) api.SourceStatusObjStatus {
	switch availObj {
	case stcode.AvailabilityStatusAvailable:
		return api.SourceStatusObjStatusAvailable
	case stcode.AvailabilityStatusTemporaryDown:
		return api.SourceStatusObjStatusTemporaryDown
	case stcode.AvailabilityStatusPermanentDown:
		return api.SourceStatusObjStatusPermanentDown
	default:
		return api.SourceStatusObjStatusUnknown
	}
}

func mapClassification(keyStateObj state.KeyStateObj) api.OptCatalogObjKeysItemClassification {
	if !keyStateObj.Classified {
		return api.OptCatalogObjKeysItemClassification{}
	}
	switch keyStateObj.Classification {
	case stcode.SourceClassGit:
		return api.NewOptCatalogObjKeysItemClassification(api.CatalogObjKeysItemClassificationGit)
	case stcode.SourceClassBrother:
		return api.NewOptCatalogObjKeysItemClassification(api.CatalogObjKeysItemClassificationBrother)
	default:
		return api.OptCatalogObjKeysItemClassification{}
	}
}

func sourceStatusOf(keyStateObj state.KeyStateObj) api.SourceStatusObj {
	return api.SourceStatusObj{
		URL:      keyStateObj.SourceURL,
		Status:   mapAvailability(keyStateObj.Availability),
		LastScan: keyStateObj.LastScan.UTC(),
	}
}
