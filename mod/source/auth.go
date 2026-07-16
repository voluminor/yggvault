package source

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

type authHeaderObj struct {
	name  string
	value string
}

type suffixRuleObj struct {
	domain string
	header authHeaderObj
}

type credentialMatcherObj struct {
	exact map[string]authHeaderObj
	rules []suffixRuleObj
}

type authTransportObj struct {
	base    http.RoundTripper
	matcher *credentialMatcherObj
	isMesh  func(host string) bool
}

// // // // // // // // // //

func (m *credentialMatcherObj) headerFor(host string) (authHeaderObj, bool) {
	if m == nil {
		return authHeaderObj{}, false
	}
	host = strings.ToLower(host)
	if kv, ok := m.exact[host]; ok {
		return kv, true
	}
	for _, rule := range m.rules {
		if host == rule.domain || strings.HasSuffix(host, "."+rule.domain) {
			return rule.header, true
		}
	}
	return authHeaderObj{}, false
}

func (m *credentialMatcherObj) empty() bool {
	return m == nil || (len(m.exact) == 0 && len(m.rules) == 0)
}

func buildCredentialMatcher(cred stconf.SourceCredentialsObj) (*credentialMatcherObj, error) {
	matcher := &credentialMatcherObj{exact: make(map[string]authHeaderObj)}

	addPredefined := func(token, domain, name, valuePrefix string) {
		if token == "" {
			return
		}
		matcher.rules = append(matcher.rules, suffixRuleObj{
			domain: domain,
			header: authHeaderObj{name: name, value: valuePrefix + token},
		})
	}
	addPredefined(cred.Github, "github.com", "Authorization", "Bearer ")
	addPredefined(cred.Gitlab, "gitlab.com", "PRIVATE-TOKEN", "")
	addPredefined(cred.Bitbucket, "bitbucket.org", "Authorization", "Bearer ")

	for rawHost, line := range cred.Others {
		host := strings.ToLower(strings.TrimSpace(rawHost))
		if host == "" {
			continue
		}
		name, value, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if !ok || name == "" || value == "" {
			return nil, fmt.Errorf("source.credentials.others[%q]: header must be in 'Name: value' form", host)
		}
		matcher.exact[host] = authHeaderObj{name: name, value: value}
	}
	return matcher, nil
}

// // // // // // // // // //

// RoundTrip sets credentials on a cloned request, preserving the RoundTripper immutability contract.
func (t *authTransportObj) RoundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Hostname()
	if t.isMesh != nil && t.isMesh(host) {
		return t.base.RoundTrip(req)
	}
	header, ok := t.matcher.headerFor(host)
	if !ok {
		return t.base.RoundTrip(req)
	}
	clone := req.Clone(req.Context())
	clone.Header.Set(header.name, header.value)
	return t.base.RoundTrip(clone)
}

func (obj *Obj) withAuth(base http.RoundTripper) http.RoundTripper {
	if obj.cred.empty() {
		return base
	}
	return &authTransportObj{base: base, matcher: obj.cred, isMesh: obj.isMeshHost}
}
