package maintenance

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func canonicalizeURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return trimmed
	}
	scheme := strings.ToLower(u.Scheme)
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	if port != "" {
		host = host + ":" + port
	}
	return (&url.URL{
		Scheme: scheme,
		Host:   host,
		Path:   strings.TrimRight(u.Path, "/"),
	}).String()
}

// ReconcileKeySources checks release_mirrors against already persisted first-source bindings.
func ReconcileKeySources(ctx context.Context, storageObj *storage.Obj, configObj *stconf.ConfigObj) (map[string]struct{}, []string, []KeySourceVerdictObj, error) {
	priorByName := make(map[string]string)
	priorByURL := make(map[string][]string)
	rowArr, listErr := storageObj.ListKeySources(ctx)
	for i := range rowArr {
		canonURL := canonicalizeURL(rowArr[i].URL)
		priorByName[rowArr[i].Key] = canonURL
		priorByURL[canonURL] = append(priorByURL[canonURL], rowArr[i].Key)
	}

	configByURL := make(map[string][]string, len(configObj.ReleaseMirrors))
	for name, rawURL := range configObj.ReleaseMirrors {
		configByURL[canonicalizeURL(rawURL)] = append(configByURL[canonicalizeURL(rawURL)], name)
	}

	suppressedSet := make(map[string]struct{})
	var e1Arr []string
	var verdictArr []KeySourceVerdictObj
	for name, rawURL := range configObj.ReleaseMirrors {
		canonURL := canonicalizeURL(rawURL)
		if priorURL, ok := priorByName[name]; ok {
			if priorURL == canonURL {
				continue
			}
			suppressedSet[name] = struct{}{}
			e1Arr = append(e1Arr, name)
			verdictArr = append(verdictArr, KeySourceVerdictObj{
				Key:    name,
				Code:   "first_source_address_changed",
				Detail: fmt.Sprintf("first-source address for key %q changed (was %q, now %q); serving last-known-good, update checks suspended until the config is corrected", name, priorURL, canonURL),
			})
			continue
		}
		if len(configByURL[canonURL]) > 1 {
			suppressedSet[name] = struct{}{}
			verdictArr = append(verdictArr, KeySourceVerdictObj{
				Key:    name,
				Code:   "config_duplicate_url",
				Detail: fmt.Sprintf("url %q is configured under multiple keys; key %q not started", canonURL, name),
			})
			continue
		}
		if priorNames := priorByURL[canonURL]; len(priorNames) > 0 {
			suppressedSet[name] = struct{}{}
			verdictArr = append(verdictArr, KeySourceVerdictObj{
				Key:    name,
				Code:   "url_bound_to_other_key",
				Detail: fmt.Sprintf("url %q is already bound to key %q; key %q not started — set the correct key name in config", canonURL, priorNames[0], name),
			})
			continue
		}
	}
	return suppressedSet, e1Arr, verdictArr, listErr
}
