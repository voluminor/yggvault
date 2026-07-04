package main

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

type reconcileVerdictObj struct {
	key    string
	code   string
	detail string
}

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

func keySourceReconcile(ctx context.Context, storageObj *storage.Obj, configObj *stconf.ConfigObj) (map[string]struct{}, []string, []reconcileVerdictObj, error) {
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
	var verdictArr []reconcileVerdictObj
	for name, rawURL := range configObj.ReleaseMirrors {
		canonURL := canonicalizeURL(rawURL)
		if priorURL, ok := priorByName[name]; ok {
			if priorURL == canonURL {
				continue
			}
			suppressedSet[name] = struct{}{}
			e1Arr = append(e1Arr, name)
			verdictArr = append(verdictArr, reconcileVerdictObj{
				key:    name,
				code:   "first_source_address_changed",
				detail: fmt.Sprintf("first-source address for key %q changed (was %q, now %q); serving last-known-good, update checks suspended until the config is corrected", name, priorURL, canonURL),
			})
			continue
		}
		if len(configByURL[canonURL]) > 1 {
			suppressedSet[name] = struct{}{}
			verdictArr = append(verdictArr, reconcileVerdictObj{
				key:    name,
				code:   "config_duplicate_url",
				detail: fmt.Sprintf("url %q is configured under multiple keys; key %q not started", canonURL, name),
			})
			continue
		}
		if priorNames := priorByURL[canonURL]; len(priorNames) > 0 {
			suppressedSet[name] = struct{}{}
			verdictArr = append(verdictArr, reconcileVerdictObj{
				key:    name,
				code:   "url_bound_to_other_key",
				detail: fmt.Sprintf("url %q is already bound to key %q; key %q not started — set the correct key name in config", canonURL, priorNames[0], name),
			})
			continue
		}
	}
	return suppressedSet, e1Arr, verdictArr, listErr
}

// // // // // // // // // //

func (rt *runtimeObj) applyKeySourceReconcile(ctx context.Context) {
	suppressedSet, e1Arr, verdictArr, listErr := keySourceReconcile(ctx, rt.storage, rt.configObj)
	zlog := rt.loggerObj.Zero()
	if listErr != nil {
		zlog.Warn().Err(listErr).Msg("key_source reconciliation skipped: could not read bindings; name-to-url changes are not enforced this start")
	}
	for i := range verdictArr {
		zlog.Error().
			Str("key", verdictArr[i].key).
			Str("reconcile", verdictArr[i].code).
			Msg(verdictArr[i].detail)
	}
	if len(suppressedSet) > 0 {
		rt.rescan.SetSuppressed(suppressedSet)
	}
	now := time.Now().UTC()
	for _, key := range e1Arr {
		_ = rt.state.MarkUnavailable(key, now)
	}
}
