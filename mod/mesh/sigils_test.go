package mesh

import (
	"strings"
	"testing"

	"github.com/voluminor/yggvault/target"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func TestResolveName(t *testing.T) {
	cases := []struct {
		name, configured, domain, host, want string
	}{
		{"configured passthrough", "my.node", "ignored.example", "h", "my.node"},
		{"domain lowercased", "", "Modules.Example.ORG", "", "modules.example.org"},
		{"invalid chars to dash", "", "a b/c", "", "a-b-c"},
		{"host fallback", "", "", "ABCDEF.pk.ygg", "abcdef.pk.ygg"},
		{"all empty -> target.Name", "", "", "", target.Name},
	}
	for _, c := range cases {
		if got := ResolveName(c.configured, c.domain, c.host); got != c.want {
			t.Errorf("%s: ResolveName(%q,%q,%q)=%q want %q", c.name, c.configured, c.domain, c.host, got, c.want)
		}
	}
}

func TestResolveNameAlwaysValid(t *testing.T) {
	got := ResolveName("", "", strings.Repeat("a", 200)+".pk.ygg")
	if len(got) < cMinNameLen || len(got) > cMaxNameLen {
		t.Fatalf("len(%q)=%d, want %d..%d", got, len(got), cMinNameLen, cMaxNameLen)
	}
}

func TestIsLocalDomain(t *testing.T) {
	for _, d := range []string{"", "localhost", "node.local", "x.internal", "127.0.0.1", "10.0.0.5", "192.168.1.1", "::1"} {
		if !isLocalDomain(d) {
			t.Errorf("isLocalDomain(%q)=false, want true", d)
		}
	}
	for _, d := range []string{"modules.example.org", "vault.example", "8.8.8.8"} {
		if isLocalDomain(d) {
			t.Errorf("isLocalDomain(%q)=true, want false", d)
		}
	}
}

func TestValidateInfoConfig(t *testing.T) {
	if err := ValidateInfoConfig(stconf.InfoObj{Name: "mirror.example", Description: "Go mirror", Contacts: map[string][]string{"abuse": {"mailto:a@x"}}}); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	if err := ValidateInfoConfig(stconf.InfoObj{}); err != nil {
		t.Fatalf("empty info rejected: %v", err)
	}
	bad := []stconf.InfoObj{
		{Name: "Bad Name"},
		{Description: " leading-space"},
		{Contacts: map[string][]string{"Bad Group": {"x@y"}}},
		{Contacts: map[string][]string{"abuse": {"ab"}}},
	}
	for i, c := range bad {
		if err := ValidateInfoConfig(c); err == nil {
			t.Errorf("bad config [%d] accepted: %+v", i, c)
		}
	}
}

func TestBuildSigils(t *testing.T) {
	cfg := &stconf.ConfigObj{}
	cfg.Web.Server.Domain = "modules.example.org"
	cfg.Info.Description = "test"

	arr, err := buildSigils(cfg, "abcdef.pk.ygg")
	if err != nil {
		t.Fatalf("buildSigils returned error: %v", err)
	}
	names := map[string]bool{}
	for _, sigilObj := range arr {
		names[sigilObj.GetName()] = true
	}
	for _, want := range []string{"yggvault", "services", "info", "inet"} {
		if !names[want] {
			t.Errorf("missing sigil %q (have %v)", want, names)
		}
	}

	cfg.Web.Server.Domain = "node.local"
	localArr, err := buildSigils(cfg, "abcdef.pk.ygg")
	if err != nil {
		t.Fatalf("buildSigils (local) returned error: %v", err)
	}
	for _, sigilObj := range localArr {
		if sigilObj.GetName() == "inet" {
			t.Error("inet published for local domain")
		}
	}
}
