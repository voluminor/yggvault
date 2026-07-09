package mesh

import (
	"crypto/ed25519"
	"fmt"
	"net"
	"strings"

	"github.com/voluminor/ratatoskr/mod/sigils"
	siginet "github.com/voluminor/ratatoskr/mod/sigils/inet"
	siginfo "github.com/voluminor/ratatoskr/mod/sigils/info"
	sigsvc "github.com/voluminor/ratatoskr/mod/sigils/services"
	yggconfig "github.com/yggdrasil-network/yggdrasil-go/src/config"

	"github.com/voluminor/yggvault/mod/mesh/yggvault"
	"github.com/voluminor/yggvault/target"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	// cWebServicePort is the fixed HTTP port advertised for the ygg entry.
	cWebServicePort = 80

	// cMinNameLen/cMaxNameLen match ratatoskr info.Name limits.
	cMinNameLen = 4
	cMaxNameLen = 64
)

// localDomainSuffixArr contains known local or non-routable suffixes that must not be published in inet.
var localDomainSuffixArr = []string{".local", ".localhost", ".internal", ".lan", ".home", ".intranet"}

// // // // // // // // // //

func normalizeName(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	var builder strings.Builder
	for _, runeValue := range text {
		switch {
		case runeValue >= 'a' && runeValue <= 'z',
			runeValue >= '0' && runeValue <= '9',
			runeValue == '.' || runeValue == '_' || runeValue == '-':
			builder.WriteRune(runeValue)
		default:
			builder.WriteByte('-')
		}
	}
	out := strings.Trim(builder.String(), "-._")
	if len(out) > cMaxNameLen {
		out = strings.Trim(out[:cMaxNameLen], "-._")
	}
	if len(out) < cMinNameLen {
		return ""
	}
	return out
}

// ResolveName returns final info.Name: configured value, normalized domain or ygg host, then stable target.Name
// fallback. The result is always valid for the info sigil.
func ResolveName(configured string, domain string, host string) string {
	if configured != "" {
		return configured
	}
	base := domain
	if base == "" {
		base = host
	}
	if name := normalizeName(base); name != "" {
		return name
	}
	return target.Name
}

func isLocalDomain(domain string) bool {
	host := strings.ToLower(strings.TrimSpace(domain))
	if host == "" || host == "localhost" {
		return true
	}
	for _, suffix := range localDomainSuffixArr {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	if ipObj := net.ParseIP(host); ipObj != nil {
		return ipObj.IsLoopback() || ipObj.IsPrivate() || ipObj.IsLinkLocalUnicast() || ipObj.IsUnspecified()
	}
	return false
}

func hostFromNodeKey(cfg *yggconfig.NodeConfig) string {
	if len(cfg.PrivateKey) != ed25519.PrivateKeySize {
		return ""
	}
	pubKey, ok := ed25519.PrivateKey(cfg.PrivateKey).Public().(ed25519.PublicKey)
	if !ok {
		return ""
	}
	return hostFromPublicKey(pubKey)
}

// // // // // // // // // //

// ValidateInfoConfig checks the identity card by ratatoskr info-sigil rules, including field and value limits.
// Empty Name is allowed because runtime supplies a default.
func ValidateInfoConfig(info stconf.InfoObj) error {
	name := info.Name
	if name == "" {
		name = target.Name
	}
	if _, err := siginfo.New(siginfo.ConfigObj{
		Name:        name,
		Type:        target.Name,
		Location:    info.Location,
		Description: info.Description,
		Contacts:    info.Contacts,
	}); err != nil {
		return fmt.Errorf("info: %w", err)
	}
	return nil
}

func buildSigils(configObj *stconf.ConfigObj, ownHost string) ([]sigils.Interface, error) {
	yggvaultObj, err := yggvault.New(target.Version, target.Hash, target.DateUpdate)
	if err != nil {
		return nil, fmt.Errorf("yggvault sigil: %w", err)
	}
	sigilArr := []sigils.Interface{yggvaultObj}

	svcObj, err := sigsvc.New(map[string]uint16{"http": cWebServicePort})
	if err != nil {
		return nil, fmt.Errorf("services sigil: %w", err)
	}
	sigilArr = append(sigilArr, svcObj)

	domain := configObj.Web.Server.Domain
	infoObj, err := siginfo.New(siginfo.ConfigObj{
		Name:        ResolveName(configObj.Info.Name, domain, ownHost),
		Type:        target.Name,
		Location:    configObj.Info.Location,
		Description: configObj.Info.Description,
		Contacts:    configObj.Info.Contacts,
	})
	if err != nil {
		return nil, fmt.Errorf("info sigil: %w", err)
	}
	sigilArr = append(sigilArr, infoObj)

	if domain != "" && !isLocalDomain(domain) {
		inetObj, err := siginet.New([]string{domain})
		if err != nil {
			return nil, fmt.Errorf("inet sigil: %w", err)
		}
		sigilArr = append(sigilArr, inetObj)
	}
	return sigilArr, nil
}
