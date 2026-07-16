package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/voluminor/yggvault/mod/brotherwire"
	"github.com/voluminor/yggvault/mod/route"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

const cMaxKeySegmentBytes = 256

var errNotVault = errors.New("source is not a vault")

// // // // // // // // // //

func instanceURLFor(rootURL string, path string) (string, error) {
	u, err := url.Parse(rootURL)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("source url %q has no host", rootURL)
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: path}).String(), nil
}

func pathSegments(rawPath string) []string {
	parts := strings.Split(strings.Trim(rawPath, "/"), "/")
	out := parts[:0]
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func validateKeySegment(seg string) (string, error) {
	if seg == "" {
		return "", fmt.Errorf("empty remote key segment")
	}
	if len(seg) > cMaxKeySegmentBytes {
		return "", fmt.Errorf("remote key segment too long: %d bytes", len(seg))
	}
	return seg, nil
}

func deriveRemoteKey(localKey, rootURL, prefix string) (string, error) {
	u, err := url.Parse(rootURL)
	if err != nil {
		return "", fmt.Errorf("parse url: %w", err)
	}
	segs := pathSegments(u.Path)

	switch {
	case len(segs) == 0:
		return localKey, nil
	case prefix != "" && segs[0] == prefix:
		if len(segs) < 2 {
			return "", fmt.Errorf("nested form under prefix %q has no key segment", prefix)
		}
		return validateKeySegment(segs[1])
	default:
		return validateKeySegment(segs[0])
	}
}

// // // // // // // // // //

type probeOutcomeObj int

const (
	probeOutcomeBody probeOutcomeObj = iota
	probeOutcomeMiss
	probeOutcomeIndeterminate
)

type probeVerdictObj int

const (
	probeVerdictVault probeVerdictObj = iota
	probeVerdictNotVault
	probeVerdictIndeterminate
)

// //

func (obj *Obj) probeHealth(ctx context.Context, healthURL string) probeVerdictObj {
	body, outcome := obj.probeBody(ctx, healthURL, cHealthMaxBytes)
	switch outcome {
	case probeOutcomeMiss:
		return probeVerdictNotVault
	case probeOutcomeIndeterminate:
		return probeVerdictIndeterminate
	}
	var payload struct {
		Service string `json:"service"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Service != cServiceName {
		return probeVerdictNotVault
	}
	return probeVerdictVault
}

func (obj *Obj) probeInfo(ctx context.Context, rootURL string) probeVerdictObj {
	infoURL, err := instanceURLFor(rootURL, route.Info)
	if err != nil {
		return probeVerdictIndeterminate
	}
	body, outcome := obj.probeBody(ctx, infoURL, cInfoMaxBytes)
	switch outcome {
	case probeOutcomeMiss:
		return probeVerdictNotVault
	case probeOutcomeIndeterminate:
		return probeVerdictIndeterminate
	}
	var payload struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(body, &payload) != nil || payload.Name == "" {
		return probeVerdictNotVault
	}
	return probeVerdictVault
}

func (obj *Obj) classifyGit(ctx context.Context, key string, rootURL string, healthURL string) error {
	switch obj.probeHealth(ctx, healthURL) {
	case probeVerdictVault:
		return nil
	case probeVerdictIndeterminate:
		return fmt.Errorf("discover %q: health probe inconclusive, classification deferred", key)
	}
	switch obj.probeInfo(ctx, rootURL) {
	case probeVerdictNotVault:
		return errNotVault
	case probeVerdictVault:
		return fmt.Errorf("discover %q: health denies vault but /info matches the contract, classification deferred", key)
	default:
		return fmt.Errorf("discover %q: health denies vault and /info probe inconclusive, classification deferred", key)
	}
}

// // // // // // // // // //

// Discover classifies a source and returns facts; rescan writes state.
// If /health confirms yggvault but Hello is invalid, this is brother unavailability, not a silent git fallback.
func (obj *Obj) Discover(ctx context.Context, key, rootURL string) (DiscoveryResultObj, error) {
	healthURL, err := instanceURLFor(rootURL, route.Health)
	if err != nil {
		return DiscoveryResultObj{}, permanent(fmt.Errorf("discover %q: %w", key, err))
	}

	switch classifyErr := obj.classifyGit(ctx, key, rootURL, healthURL); {
	case errors.Is(classifyErr, errNotVault):
		return DiscoveryResultObj{
			Class:     stcode.SourceClassGit,
			RemoteKey: key,
			SourceURL: rootURL,
		}, nil
	case classifyErr != nil:
		return DiscoveryResultObj{}, classifyErr
	}

	webAddr, yggAddr, routePrefix, infoOK := obj.fetchBrotherInfo(ctx, rootURL)

	remotePrefix := obj.routingPrefix
	if routePrefix != nil {
		remotePrefix = *routePrefix
	}
	remoteKey, err := deriveRemoteKey(key, rootURL, remotePrefix)
	if err != nil {
		return DiscoveryResultObj{}, stcode.NewErrBrotherContractNotConfirmed(err.Error(), key, rootURL)
	}

	if infoOK {
		if err := obj.checkAddrDivergence(rootURL, webAddr, yggAddr); err != nil {
			return DiscoveryResultObj{}, stcode.NewErrBrotherContractNotConfirmed("info divergence: "+err.Error(), key, rootURL)
		}
	}

	session, err := obj.BrotherDial(ctx, key, remoteKey, rootURL)
	if err != nil {
		return DiscoveryResultObj{
			Class:      stcode.SourceClassBrother,
			RemoteKey:  remoteKey,
			SourceURL:  rootURL,
			BrotherURL: rootURL,
			WebAddr:    webAddr,
			YggAddr:    yggAddr,
		}, nil
	}
	defer session.Close()

	hello, err := session.Hello(ctx)
	if err != nil {
		return DiscoveryResultObj{}, stcode.NewErrBrotherContractNotConfirmed("hello: "+err.Error(), key, rootURL)
	}
	if hello.Protocol != brotherwire.Protocol {
		return DiscoveryResultObj{}, stcode.NewErrBrotherContractNotConfirmed(
			fmt.Sprintf("protocol mismatch: got %q want %q", hello.Protocol, brotherwire.Protocol), key, rootURL)
	}

	return DiscoveryResultObj{
		Class:      stcode.SourceClassBrother,
		RemoteKey:  remoteKey,
		SourceURL:  rootURL,
		BrotherURL: rootURL,
		WebAddr:    webAddr,
		YggAddr:    yggAddr,
	}, nil
}
