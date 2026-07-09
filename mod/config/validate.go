package config

import (
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/mod/mesh"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func validateHTTPURL(field, raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("%s is not a valid URL: %w", field, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("%s must be an http or https URL", field)
	}
	if parsed.Host == "" {
		return fmt.Errorf("%s must include a host", field)
	}
	return nil
}

// // // // // // // // // //

func validate(stc *stcfg.ConfigObj) error {
	for _, step := range []func(*stcfg.ConfigObj) error{
		validateRegistry,
		validateLogging,
		validateRouting,
		validateCache,
		validateMetrics,
		validateProfiling,
		validateRateLimit,
		validateSource,
		validateWeb,
		validateInfo,
		validateTopLevel,
		validatePaths,
		validateFiles,
	} {
		if err := step(stc); err != nil {
			return err
		}
	}
	return nil
}

// //

func validateInfo(stc *stcfg.ConfigObj) error {
	return mesh.ValidateInfoConfig(stc.Info)
}

func validateRegistry(stc *stcfg.ConfigObj) error {
	if len(stc.ReleaseMirrors) == 0 {
		return errors.New("release_mirrors must contain at least one entry")
	}
	for k, sourceURL := range stc.ReleaseMirrors {
		if k == "" {
			return errors.New("release_mirrors: key must not be empty")
		}
		if reservedKeySetObj[k] {
			return fmt.Errorf("release_mirrors: key %q is reserved", k)
		}
		if goMajorKeyPatternObj.MatchString(k) {
			return fmt.Errorf("release_mirrors: key %q must not look like a Go module major version suffix (v2, v3, ...)", k)
		}
		if !keyPatternObj.MatchString(k) {
			return fmt.Errorf("release_mirrors: key %q does not match required pattern", k)
		}
		if k == strings.TrimSpace(stc.Web.Server.Domain) {
			return fmt.Errorf("release_mirrors: key %q must not equal web.server.domain", k)
		}
		if strings.TrimSpace(sourceURL) == "" {
			return fmt.Errorf("release_mirrors: source URL for key %q must not be empty", k)
		}
		if err := validateHTTPURL(fmt.Sprintf("release_mirrors[%q]", k), sourceURL); err != nil {
			return err
		}
	}
	return nil
}

// //

func validateLogging(stc *stcfg.ConfigObj) error {
	if !stc.Logging.Console.Enabled && !stc.Logging.File.Enabled && !stc.Logging.Victorialogs.Enabled {
		return errors.New("at least one logging sink must be enabled")
	}
	if stc.Logging.Victorialogs.Enabled {
		if strings.TrimSpace(stc.Logging.Victorialogs.Url) == "" {
			return errors.New("logging.victorialogs.url is required when victorialogs sink is enabled")
		}
		if err := validateHTTPURL("logging.victorialogs.url", stc.Logging.Victorialogs.Url); err != nil {
			return err
		}
		if uint64(stc.Logging.Victorialogs.BatchMaxBytes) < 1 {
			return errors.New("logging.victorialogs.batch_max_bytes must be >= 1")
		}
		if uint64(stc.Logging.Victorialogs.BatchMaxBytes) > cMaxVictorialogsBatchBytes {
			return fmt.Errorf("logging.victorialogs.batch_max_bytes must be <= %d", cMaxVictorialogsBatchBytes)
		}
		if stc.Logging.Victorialogs.BufferMaxLines > cMaxVictorialogsBufferLines {
			return fmt.Errorf("logging.victorialogs.buffer_max_lines must be <= %d", cMaxVictorialogsBufferLines)
		}
	}
	if !stc.Logging.File.Enabled {
		return nil
	}
	if strings.TrimSpace(stc.Logging.File.Dir) == "" {
		return errors.New("logging.file.dir is required when file sink is enabled")
	}
	if uint64(stc.Logging.File.MaxSize) < 1 {
		return errors.New("logging.file.max_size must be >= 1")
	}
	if stc.Logging.File.MaxBackups < 1 {
		return errors.New("logging.file.max_backups must be >= 1")
	}
	if stc.Logging.File.MaxAge < 24*time.Hour {
		return errors.New("logging.file.max_age must be >= 1 day")
	}
	return nil
}

// //

func validateRouting(stc *stcfg.ConfigObj) error {
	if !routingPrefixPatternObj.MatchString(stc.Web.Routing.Prefix) {
		return fmt.Errorf("web.routing.prefix invalid: %q", stc.Web.Routing.Prefix)
	}

	switch stc.Web.Routing.Prefix {
	case "health", "info", "metrics", "openapi.json",
		"og", "logo", "og.png", "favicon.ico", "sitemap.xml":
		return fmt.Errorf("web.routing.prefix must not be a reserved path: %q", stc.Web.Routing.Prefix)
	}

	if uint64(stc.Web.Ingress.ReadBufferSize) < uint64(stc.Web.Ingress.MaxRequestUriBytes)+1024 {
		return errors.New("web.ingress.read_buffer_size must exceed max_request_uri_bytes by at least 1024")
	}

	return nil
}

// //

func validateRateLimit(stc *stcfg.ConfigObj) error {
	burstBuckets := []struct {
		name  string
		rps   uint
		burst uint
	}{
		{"web.http", stc.RateLimit.Web.Http.RequestsPerSecond, stc.RateLimit.Web.Http.Burst},
		{"web.https", stc.RateLimit.Web.Https.RequestsPerSecond, stc.RateLimit.Web.Https.Burst},
		{"ygg", stc.RateLimit.Ygg.RequestsPerSecond, stc.RateLimit.Ygg.Burst},
		{"web.http.per_peer", stc.RateLimit.Web.Http.PerPeer.RequestsPerSecond, stc.RateLimit.Web.Http.PerPeer.Burst},
		{"web.https.per_peer", stc.RateLimit.Web.Https.PerPeer.RequestsPerSecond, stc.RateLimit.Web.Https.PerPeer.Burst},
		{"ygg.per_peer", stc.RateLimit.Ygg.PerPeer.RequestsPerSecond, stc.RateLimit.Ygg.PerPeer.Burst},
	}
	for _, b := range burstBuckets {
		if b.rps > 0 && b.burst < 1 {
			return fmt.Errorf("rate_limit.%s.burst must be >= 1 when requests_per_second > 0", b.name)
		}
	}
	peerBuckets := []struct {
		name       string
		rps        uint
		maxTracked uint
	}{
		{"web.http.per_peer", stc.RateLimit.Web.Http.PerPeer.RequestsPerSecond, stc.RateLimit.Web.Http.PerPeer.MaxTracked},
		{"web.https.per_peer", stc.RateLimit.Web.Https.PerPeer.RequestsPerSecond, stc.RateLimit.Web.Https.PerPeer.MaxTracked},
		{"ygg.per_peer", stc.RateLimit.Ygg.PerPeer.RequestsPerSecond, stc.RateLimit.Ygg.PerPeer.MaxTracked},
	}
	for _, p := range peerBuckets {
		if p.rps > 0 && p.maxTracked < 1 {
			return fmt.Errorf("rate_limit.%s.max_tracked must be >= 1 when requests_per_second > 0", p.name)
		}
	}
	return nil
}

func validateSource(stc *stcfg.ConfigObj) error {
	if stc.Source.RateLimit.RequestsPerSecond > 0 && stc.Source.RateLimit.Burst < 1 {
		return errors.New("source.rate_limit.burst must be >= 1 when requests_per_second > 0")
	}
	return nil
}

// //

func validateMetrics(stc *stcfg.ConfigObj) error {
	if stc.Metrics.Push.Enabled {
		if strings.TrimSpace(stc.Metrics.Push.Url) == "" {
			return errors.New("metrics.push.url is required when metrics push is enabled")
		}
		if err := validateHTTPURL("metrics.push.url", stc.Metrics.Push.Url); err != nil {
			return err
		}
	}
	return nil
}

// //

func validateProfiling(stc *stcfg.ConfigObj) error {
	if !stc.Profiling.Enabled {
		return nil
	}
	listen := strings.TrimSpace(stc.Profiling.Listen)
	if listen == "" {
		return errors.New("profiling.listen is required when profiling is enabled")
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return fmt.Errorf("profiling.listen must be a valid host:port: %w", err)
	}
	if strings.TrimSpace(port) == "" {
		return errors.New("profiling.listen must include a port")
	}
	if !isLoopbackHost(host) {
		return fmt.Errorf("profiling.listen must bind a loopback address (got host %q); reach it externally via an ssh tunnel", host)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ipObj := net.ParseIP(host)
	return ipObj != nil && ipObj.IsLoopback()
}

// //

func validateWeb(stc *stcfg.ConfigObj) error {
	state, err := webStatus(&stc.Web.Server)
	if err != nil {
		return err
	}

	cert := strings.TrimSpace(stc.Web.Server.Tls.Cert)
	key := strings.TrimSpace(stc.Web.Server.Tls.Key)
	if (cert == "") != (key == "") {
		return errors.New("web.server.tls.cert and tls.key must both be set or both empty")
	}

	if state == webStateDisabled {
		return nil
	}

	domainText := strings.TrimSpace(stc.Web.Server.Domain)
	if domainText == "" {
		return errors.New("web.server.domain is required when web ingress is enabled")
	}
	// A domain with a port/scheme/path is not supported: it is woven into the module path
	// (targetModulePath) and into links, where colon and slash are invalid.
	if strings.ContainsAny(domainText, ":/") {
		return errors.New("web.server.domain must be a bare hostname without scheme, port or path")
	}
	return nil
}

type webState int

const (
	webStateDisabled webState = iota
	webStateEnabled
)

func webStatus(s *stcfg.WebServerObj) (webState, error) {
	switch s.Mode {
	case stcfg.WebServerModeShared:
		if strings.TrimSpace(s.Shared.Listen) == "" {
			return webStateDisabled, nil
		}
	case stcfg.WebServerModeSplit:
		http := strings.TrimSpace(s.Split.Http.Listen)
		https := strings.TrimSpace(s.Split.Https.Listen)
		if http == "" && https == "" {
			return webStateDisabled, nil
		}
		if http == "" {
			return webStateDisabled, errors.New("web.server.split.http.listen is required for mode=split")
		}
		if https == "" {
			return webStateDisabled, errors.New("web.server.split.https.listen is required for mode=split")
		}
	case stcfg.WebServerModeSingle:
		listen := strings.TrimSpace(s.Single.Listen)
		if listen == "" {
			return webStateDisabled, nil
		}
		if !s.Single.Proto.IsValid() {
			return webStateDisabled, errors.New("web.server.single.proto is required for mode=single")
		}
	default:
		return webStateDisabled, errors.New("web.server.mode must be one of shared|split|single")
	}
	return webStateEnabled, nil
}

// //

func validateCache(stc *stcfg.ConfigObj) error {
	if stc.Source.Retry.BackoffMax < stc.Source.Retry.BackoffInitial {
		return errors.New("source.retry.backoff_max must be >= backoff_initial")
	}
	if uint64(stc.Storage.ArchiveLimits.Size.PerFile) > uint64(stc.Storage.ArchiveLimits.Size.Unpacked) {
		return errors.New("storage.archive_limits.size.per_file must be <= storage.archive_limits.size.unpacked")
	}
	if uint64(stc.Storage.Quota.MaxTotalSize) > 0 {
		if uint64(stc.Storage.Quota.EvictToSize) < 1 {
			return errors.New("storage.quota.evict_to_size must be >= 1 when quota is enabled")
		}
		if uint64(stc.Storage.Quota.EvictToSize) >= uint64(stc.Storage.Quota.MaxTotalSize) {
			return errors.New("storage.quota.evict_to_size must be lower than storage.quota.max_total_size when quota is enabled")
		}
	}
	if mv := stc.Storage.Quota.MaxVersionsPerKey; mv > 0 && mv < stc.Storage.Quota.RetainLatestPerKey {
		return errors.New("storage.quota.max_versions_per_key must be 0 or >= retain_latest_per_key")
	}
	if stc.Rescan.Verify.ArchiveInterval < stc.Rescan.Verify.RecentInterval {
		return errors.New("rescan.verify.archive_interval must be >= rescan.verify.recent_interval")
	}
	return nil
}

// //

func validateTopLevel(stc *stcfg.ConfigObj) error {
	state, _ := webStatus(&stc.Web.Server)
	ygg := strings.TrimSpace(stc.Ygg.PemKey) != ""
	if state == webStateDisabled && !ygg {
		return errors.New("at least one ingress (web or ygg) must be enabled")
	}
	if !historyPrefixPatternObj.MatchString(stc.HistoryPolicy.Prefix) {
		return fmt.Errorf("history_policy.prefix invalid: %q", stc.HistoryPolicy.Prefix)
	}
	return nil
}

// //

func validatePaths(stc *stcfg.ConfigObj) error {
	if err := validateDeny(stc); err != nil {
		return err
	}
	storageDir := strings.TrimSpace(stc.Storage.Dir)
	static := strings.TrimSpace(stc.Web.Static.Dir)
	loggingDir := ""
	if stc.Logging.File.Enabled {
		loggingDir = strings.TrimSpace(stc.Logging.File.Dir)
	}
	for _, pair := range []struct {
		left, right string
		message     string
	}{
		{storageDir, loggingDir, "storage.dir and logging.file.dir must not overlap"},
		{static, loggingDir, "web.static.dir and logging.file.dir must not overlap"},
		{static, storageDir, "storage.dir and web.static.dir must not overlap"},
	} {
		if pair.left == "" || pair.right == "" {
			continue
		}
		conflict, err := util.PathsOverlap(pair.left, pair.right)
		if err != nil {
			return err
		}
		if conflict {
			return errors.New(pair.message)
		}
	}
	return nil
}

func validateDeny(stc *stcfg.ConfigObj) error {
	for i, raw := range stc.Web.Static.Deny {
		rule := strings.TrimSpace(raw)
		if rule == "" {
			return fmt.Errorf("web.static.deny[%d]: rule must not be empty", i)
		}
		if strings.Contains(rule, "..") || strings.Contains(rule, `\`) {
			return fmt.Errorf("web.static.deny[%d]: rule must not contain '..' or backslash", i)
		}
		if strings.HasPrefix(rule, ".") {
			if strings.Contains(rule, "/") {
				return fmt.Errorf("web.static.deny[%d]: extension rule must not contain '/'", i)
			}
			continue
		}
		if !strings.HasPrefix(rule, "/") {
			return fmt.Errorf("web.static.deny[%d]: rule must start with '.' (extension) or '/' (path)", i)
		}
	}
	return nil
}

// //

func validateFiles(stc *stcfg.ConfigObj) error {
	if static := strings.TrimSpace(stc.Web.Static.Dir); static != "" {
		info, err := os.Stat(static)
		if err != nil {
			return fmt.Errorf("web.static.dir: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("web.static.dir: %q is not a directory", static)
		}
		indexFile, err := validateStaticIndexFile(stc.Web.Static.IndexFile)
		if err != nil {
			return err
		}
		indexPath, err := util.JoinInside(static, indexFile)
		if err != nil {
			return fmt.Errorf("web.static.index_file: %w", err)
		}
		if _, err = os.Stat(indexPath); err != nil {
			return fmt.Errorf("web.static.index_file: %w", err)
		}
	}

	cert := strings.TrimSpace(stc.Web.Server.Tls.Cert)
	key := strings.TrimSpace(stc.Web.Server.Tls.Key)
	if cert != "" && key != "" {
		if _, err := tls.LoadX509KeyPair(cert, key); err != nil {
			return fmt.Errorf("tls cert/key: %w", err)
		}
	}
	return nil
}

func validateStaticIndexFile(indexFile string) (string, error) {
	trimmed := strings.TrimSpace(indexFile)
	if trimmed == "" {
		return "", errors.New("web.static.index_file must not be empty")
	}

	cleanPath, err := util.CleanArchiveEntryPath(trimmed)
	if err != nil {
		return "", fmt.Errorf("web.static.index_file: %w", err)
	}
	if cleanPath != trimmed {
		return "", errors.New("web.static.index_file must be a clean relative path")
	}

	return cleanPath, nil
}
