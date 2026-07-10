package view

import (
	"html/template"
	"strconv"
	"strings"
)

// // // // // // // // // //

type (
	// CatalogObj is the external input for catalog.html.
	CatalogObj struct {
		Context ContextObj
		Keys    []CatalogEntryObj
	}

	// CatalogEntryObj describes one package key in the catalog input.
	CatalogEntryObj struct {
		Key           string
		LatestVersion string
		Source        SourceObj
		// Go and Composer mark ecosystems serving this key's latest version.
		Go       bool
		Composer bool
	}
)

// // // // // // // // // //

type (
	catalogKeyObj struct {
		Key, URL, Latest string
		Source           SourceObj
		Go, Composer     bool
	}

	contactValueObj struct {
		Text, URL string
	}

	contactRowObj struct {
		Name   string
		Values []contactValueObj
	}

	aboutObj struct {
		Description string
		Location    string
		Contacts    []contactRowObj
	}

	catalogTemplateObj struct {
		Head         headObj
		About        aboutObj
		Count        int
		OnlineCount  int
		ProblemCount int
		HealthStatus string
		Keys         []catalogKeyObj
	}
)

// // // // // // // // // //

// aboutBlock builds the public node card; http(s) contacts become links.
func aboutBlock(serviceObj ServiceObj) aboutObj {
	outObj := aboutObj{Description: serviceObj.Description, Location: serviceObj.Location}
	for _, groupObj := range serviceObj.Contacts {
		rowObj := contactRowObj{Name: groupObj.Name}
		for _, valueText := range groupObj.Values {
			valueObj := contactValueObj{Text: valueText}
			if strings.HasPrefix(valueText, "https://") || strings.HasPrefix(valueText, "http://") {
				valueObj.URL = valueText
			}
			rowObj.Values = append(rowObj.Values, valueObj)
		}
		if len(rowObj.Values) > 0 {
			outObj.Contacts = append(outObj.Contacts, rowObj)
		}
	}
	return outObj
}

// // // // // // // // // //

// buildCatalog derives template-only fields for catalog.html.
func buildCatalog(inputObj CatalogObj, css template.CSS) catalogTemplateObj {
	keyArr := make([]catalogKeyObj, 0, len(inputObj.Keys))
	for i := range inputObj.Keys {
		entryObj := inputObj.Keys[i]
		keyArr = append(keyArr, catalogKeyObj{
			Key:      entryObj.Key,
			URL:      homePath(inputObj.Context, entryObj.Key),
			Latest:   entryObj.LatestVersion,
			Source:   normalizeSource(entryObj.Source),
			Go:       entryObj.Go,
			Composer: entryObj.Composer,
		})
	}
	onlineCount := 0
	for i := range keyArr {
		if keyArr[i].Source.Status == "available" {
			onlineCount++
		}
	}
	problemCount := len(keyArr) - onlineCount
	healthStatus := "ok"
	if problemCount > 0 {
		healthStatus = "temporary_down"
	}
	desc := inputObj.Context.Service.Tagline + " · " + strconv.Itoa(len(keyArr)) + " packages mirrored · " +
		strconv.Itoa(onlineCount) + " sources online · served over regular web and yggdrasil mesh"
	return catalogTemplateObj{
		Head:         head(inputObj.Context, css, inputObj.Context.Service.Name+" - vault index", desc, "og.png", cleanHome(inputObj.Context), homePath(inputObj.Context, "feed.xml")),
		About:        aboutBlock(inputObj.Context.Service),
		Count:        len(keyArr),
		OnlineCount:  onlineCount,
		ProblemCount: problemCount,
		HealthStatus: healthStatus,
		Keys:         keyArr,
	}
}
