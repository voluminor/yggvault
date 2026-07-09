#!/usr/bin/env bash
# Live verifier: imports packages THROUGH the running nodes using the COMMITTED consumer fixtures in
# tests/stub (copied into the image at /stub). All scratch lives in the container (/work, user-owned);
# results go to /out (a bind mount into tmp/). Exit non-zero on any failure.
#
# Every node shares the canonical domain vault.test, so the single Go fixture imports vault.test/errors
# regardless of which node (seed or brother) answers.
set -uo pipefail

OUT=/out
mkdir -p "$OUT"
RESULT="$OUT/verify-results.txt"
: > "$RESULT"
PASS=0; FAIL=0
# Seed nodes poll GitHub every 10m to stay within unauthenticated rate limits; verifier waits
# long enough for one retry after a transient first-cycle source failure.
WAIT_5S_TRIES=${WAIT_5S_TRIES:-150}

log(){ echo "[verify] $*" | tee -a "$RESULT"; }
ok(){   PASS=$((PASS+1)); echo "  PASS: $*" | tee -a "$RESULT"; }
bad(){  FAIL=$((FAIL+1)); echo "  FAIL: $*" | tee -a "$RESULT"; }

ensure_rpc_web_probe(){
  if [ -x /work/rpc-web-check ]; then return 0; fi
  cat > /work/rpc-web-check.go <<'GO'
package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"os"
	"time"
)

type helloReplyObj struct {
	Protocol              string
	MaxFetchResponseBytes uint64
	MaxFetchBatchCount    uint32
}

func main() {
	if len(os.Args) != 2 && len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: rpc-web-check host:port [key]")
		os.Exit(2)
	}
	addr := os.Args[1]
	key := ""
	if len(os.Args) == 3 {
		key = os.Args[2]
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial %s: %v\n", addr, err)
		os.Exit(1)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err = fmt.Fprintf(conn, "CONNECT /rpc HTTP/1.0\r\nHost: %s\r\n\r\n", addr); err != nil {
		fmt.Fprintf(os.Stderr, "write CONNECT: %v\n", err)
		os.Exit(1)
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: http.MethodConnect})
	if err != nil {
		fmt.Fprintf(os.Stderr, "read CONNECT response: %v\n", err)
		os.Exit(1)
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "CONNECT status=%d\n", resp.StatusCode)
		os.Exit(1)
	}
	client := rpc.NewClient(conn)
	defer client.Close()
	var reply helloReplyObj
	if err = client.Call("Brother.Hello", struct{}{}, &reply); err != nil {
		fmt.Fprintf(os.Stderr, "Brother.Hello: %v\n", err)
		os.Exit(1)
	}
	if reply.Protocol != "mesh1" {
		fmt.Fprintf(os.Stderr, "protocol=%q\n", reply.Protocol)
		os.Exit(1)
	}
	if reply.MaxFetchResponseBytes == 0 || reply.MaxFetchBatchCount == 0 {
		fmt.Fprintf(os.Stderr, "invalid limits: bytes=%d count=%d\n", reply.MaxFetchResponseBytes, reply.MaxFetchBatchCount)
		os.Exit(1)
	}
	if key == "" {
		fmt.Printf("protocol=%s fetch_bytes=%d batch_count=%d", reply.Protocol, reply.MaxFetchResponseBytes, reply.MaxFetchBatchCount)
		return
	}
	type hashWireObj [24]byte
	type indexArgObj struct {
		Key  string
		Page uint32
	}
	type indexEntryObj struct {
		Version     string
		BodyMD      string
		TreeHash    hashWireObj
		SourceHash  hashWireObj
		UpstreamSeq int64
	}
	type sourceInfoObj struct {
		SourceURL string
	}
	type indexReplyObj struct {
		Entries  []indexEntryObj
		NextPage uint32
		Source   sourceInfoObj
	}
	var idx indexReplyObj
	if err = client.Call("Brother.Index", indexArgObj{Key: key}, &idx); err != nil {
		fmt.Fprintf(os.Stderr, "Brother.Index: %v\n", err)
		os.Exit(1)
	}
	if len(idx.Entries) == 0 {
		fmt.Fprintf(os.Stderr, "Brother.Index returned empty page for %q\n", key)
		os.Exit(1)
	}
	type versionArgObj struct {
		Key     string
		Version string
	}
	type versionReplyObj struct {
		TreeHash  hashWireObj
		TreeBytes []byte
	}
	var ver versionReplyObj
	if err = client.Call("Brother.Version", versionArgObj{Key: key, Version: idx.Entries[0].Version}, &ver); err != nil {
		fmt.Fprintf(os.Stderr, "Brother.Version: %v\n", err)
		os.Exit(1)
	}
	if len(ver.TreeBytes) == 0 {
		fmt.Fprintf(os.Stderr, "Brother.Version returned empty tree for %q %q\n", key, idx.Entries[0].Version)
		os.Exit(1)
	}
	fmt.Printf("protocol=%s entries=%d version=%s tree_bytes=%d fetch_bytes=%d batch_count=%d", reply.Protocol, len(idx.Entries), idx.Entries[0].Version, len(ver.TreeBytes), reply.MaxFetchResponseBytes, reply.MaxFetchBatchCount)
}
GO
  GOCACHE=/work/.gocache GOPATH=/work/.gopath go build -o /work/rpc-web-check /work/rpc-web-check.go >>"$RESULT" 2>&1
}

check_rpc_web(){
  local addr=$1 label=$2 out
  log "RPC(web) $label  via $addr"
  if ! ensure_rpc_web_probe; then bad "RPC(web) $label probe build failed"; return; fi
  if out=$(/work/rpc-web-check "$addr" 2>>"$RESULT"); then
    ok "RPC(web) $label Hello ok ($out)"
  else
    bad "RPC(web) $label Hello failed via $addr"
  fi
}

check_rpc_web_data(){
  local addr=$1 label=$2 key=$3 out
  log "RPC(web data) $label/$key  via $addr"
  if ! wait_key "http://$addr" "$key"; then bad "RPC(web data) $label/$key key never appeared"; return; fi
  if ! ensure_rpc_web_probe; then bad "RPC(web data) $label/$key probe build failed"; return; fi
  if out=$(/work/rpc-web-check "$addr" "$key" 2>>"$RESULT"); then
    ok "RPC(web data) $label/$key Index+Version ok ($out)"
  else
    bad "RPC(web data) $label/$key Index+Version failed via $addr"
  fi
}

wait_key(){ # wait until <url>/<key>/@v/list is non-empty (ingest / brother-sync settled)
  local url=$1 key=$2 tries=${3:-$WAIT_5S_TRIES}
  for i in $(seq 1 "$tries"); do
    if curl -fsS -m5 "$url/$key/@v/list" 2>/dev/null | grep -q .; then return 0; fi
    sleep 5
  done
  return 1
}

wait_p2(){ # wait until <url>/p2/<pkg>.json advertises at least one version
  local url=$1 pkg=$2 tries=${3:-$WAIT_5S_TRIES}
  for i in $(seq 1 "$tries"); do
    if curl -fsS -m5 "$url/p2/$pkg.json" 2>/dev/null | grep -q version; then return 0; fi
    sleep 5
  done
  return 1
}

check_go(){ # url key  -> copy committed Go fixture, go get + build (-tags verify) vault.test/<key> via GOPROXY=url
  local url=$1 key=$2 mod="vault.test/$2"
  log "GO  $mod  via $url"
  if ! wait_key "$url" "$key"; then bad "GO $mod: key never appeared at $url"; return; fi
  # unique scratch per node; cd to a stable dir BEFORE rm so we never delete our own cwd
  local tag; tag=$(echo "$url" | tr -dc 'a-z0-9')
  local d="/work/go-${tag}-${key}"
  cd /work; rm -rf "$d"; cp -r /stub/go "$d"; cd "$d" || { bad "GO $mod: scratch setup failed"; return; }
  export GOPATH="$d/.gp" GOCACHE="$d/.gc" GOMODCACHE="$d/.gp/pkg/mod"
  export GOPROXY="$url" GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=
  local ver; ver=$(curl -fsS -m5 "$url/$key/@v/list" | head -1)
  if go get "$mod@$ver" >>"$RESULT" 2>&1 && go build -tags verify -o app . >>"$RESULT" 2>&1; then
    local out; out=$(./app 2>&1)
    if echo "$out" | grep -q "import OK"; then
      ok "GO $mod@$ver built+ran via $url -> '$out' (go.sum: $(grep -c vault.test go.sum) entries)"
    else bad "GO $mod@$ver built but ran unexpectedly: '$out'"; fi
  else bad "GO $mod@$ver get/build failed via $url"; fi
  unset GOPATH GOCACHE GOMODCACHE GOPROXY GOSUMDB GOWORK GOTOOLCHAIN
}

check_composer(){ # url pkg vkey -> copy committed composer fixture, install with node as ONLY repo (+ dist sha1)
  local url=$1 pkg=$2 vkey=$3
  log "PHP $pkg  via $url"
  if ! wait_p2 "$url" "$pkg"; then bad "PHP $pkg: /p2 never appeared at $url"; return; fi
  local d="/work/php-$(echo "$pkg" | tr / -)"; rm -rf "$d"; mkdir -p "$d"; cd "$d"
  export COMPOSER_HOME="$d/.composer" COMPOSER_NO_INTERACTION=1
  # committed require/config + per-node repository (vault only; packagist disabled)
  jq --arg u "$url" '.repositories=[{"type":"composer","url":$u},{"packagist.org":false}]' \
     /stub/composer/composer.json > composer.json
  if composer update --no-interaction --ignore-platform-reqs >>"$RESULT" 2>&1; then
    ok "PHP $pkg installed from $url (vendor: $(ls vendor 2>/dev/null | tr '\n' ' '))"
  elif [ -f composer.lock ]; then
    ok "PHP $pkg resolved+locked $(jq -r '.packages|length' composer.lock 2>/dev/null) pkgs from $url (dist download skipped: canonical https domain)"
  else bad "PHP $pkg resolve failed from $url"; fi
  # dist sha1: advertised in /p2 == served zip bytes
  local ver adv got
  ver=$(curl -fsS -m5 "$url/p2/$pkg.json" | jq -r --arg p "$pkg" '.packages[$p][0].version')
  adv=$(curl -fsS -m5 "$url/p2/$pkg.json" | jq -r --arg p "$pkg" '.packages[$p][0].dist.shasum')
  got=$(curl -fsS -m15 "$url/$vkey/$ver.zip" | sha1sum | cut -d' ' -f1)
  if [ -n "$adv" ] && [ "$adv" = "$got" ]; then ok "PHP $pkg dist sha1 match ($ver)"; else bad "PHP $pkg dist sha1 mismatch adv=$adv got=$got"; fi
  unset COMPOSER_HOME COMPOSER_NO_INTERACTION
}

check_go_tls(){ # canonical https GOPROXY through the TLS edge: real https module fetch over vault.test
  local base="https://vault.test" key="errors" mod="vault.test/errors"
  log "GO(TLS) $mod  via $base (edge -> node-a)"
  if ! wait_key "http://node-a:8080" "$key"; then bad "GO(TLS): seed key never appeared"; return; fi
  local d="/work/gotls-$key"; cd /work; rm -rf "$d"; cp -r /stub/go "$d"; cd "$d" || { bad "GO(TLS): scratch setup failed"; return; }
  export GOPATH="$d/.gp" GOCACHE="$d/.gc" GOMODCACHE="$d/.gp/pkg/mod"
  export GOPROXY="$base" GOSUMDB=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=
  export SSL_CERT_FILE=/certs/ca.crt   # Go honors SSL_CERT_FILE -> trust the test CA for https
  # GOPROXY canonical path = base + full module path (vault.test/errors), same as `go get` fetches.
  local ver; ver=$(curl --cacert /certs/ca.crt -fsS -m10 "$base/$mod/@v/list" | head -1)
  if [ -n "$ver" ] && go get "$mod@$ver" >>"$RESULT" 2>&1 && go build -tags verify -o app . >>"$RESULT" 2>&1 \
     && ./app 2>&1 | grep -q "import OK"; then
    ok "GO(TLS) $mod@$ver fetched+built over https://vault.test (real TLS, CA-verified)"
  else bad "GO(TLS) $mod get/build failed over https://vault.test"; fi
  unset GOPATH GOCACHE GOMODCACHE GOPROXY GOSUMDB GOWORK GOTOOLCHAIN SSL_CERT_FILE
}

check_composer_tls(){ # composer over https://vault.test: REAL dist download + extract (the future-work goal)
  local base="https://vault.test" pkg="monolog/monolog" vkey="monolog"
  log "PHP(TLS) $pkg  via $base (real dist download + extract)"
  if ! curl --cacert /certs/ca.crt -fsS -m10 "$base/p2/$pkg.json" 2>/dev/null | grep -q version; then bad "PHP(TLS) $pkg: /p2 empty via edge"; return; fi
  local d="/work/phptls-$(echo "$pkg" | tr / -)"; rm -rf "$d"; mkdir -p "$d"; cd "$d"
  export COMPOSER_HOME="$d/.composer" COMPOSER_NO_INTERACTION=1
  # vault as the ONLY repo over https; secure-http stays ON (real TLS), no http bypass
  jq --arg u "$base" '.repositories=[{"type":"composer","url":$u},{"packagist.org":false}] | del(.config["secure-http"])' \
     /stub/composer/composer.json > composer.json
  # php trusts the test CA via openssl.cafile -> verified TLS dist download
  if php -d openssl.cafile=/certs/ca.crt /usr/local/bin/composer update --no-interaction --ignore-platform-reqs >>"$RESULT" 2>&1; then
    if [ -f "vendor/$pkg/composer.json" ] && [ -d "vendor/psr/log" ]; then
      ok "PHP(TLS) $pkg downloaded+extracted dist over https ($(find vendor -name '*.php' | wc -l) php files, deps resolved)"
    else bad "PHP(TLS) $pkg installed but dist not extracted (vendor/$pkg missing)"; fi
  else bad "PHP(TLS) $pkg composer update failed over https://vault.test"; fi
  # dist sha1 over the canonical https path: advertised in /p2 == served zip bytes
  local ver adv got
  ver=$(curl --cacert /certs/ca.crt -fsS -m10 "$base/p2/$pkg.json" | jq -r --arg p "$pkg" '.packages[$p][0].version')
  adv=$(curl --cacert /certs/ca.crt -fsS -m10 "$base/p2/$pkg.json" | jq -r --arg p "$pkg" '.packages[$p][0].dist.shasum')
  got=$(curl --cacert /certs/ca.crt -fsS -m15 "$base/$vkey/$ver.zip" | sha1sum | cut -d' ' -f1)
  if [ -n "$adv" ] && [ "$adv" = "$got" ]; then ok "PHP(TLS) $pkg dist sha1 match over https ($ver)"; else bad "PHP(TLS) $pkg dist sha1 mismatch adv=$adv got=$got"; fi
  unset COMPOSER_HOME COMPOSER_NO_INTERACTION
}

check_bazel(){ # url key -> verify the rendered http_archive() snippet against the served universal .tar.gz
  local url=$1 key=$2
  log "BAZEL $key  via $url"
  if ! wait_key "$url" "$key"; then bad "BAZEL $key: key never appeared at $url"; return; fi
  local ver; ver=$(curl -fsS -m5 "$url/$key/@v/list" | head -1)
  local html; html=$(curl -fsS -m10 "$url/$key/$ver")
  if ! echo "$html" | grep -q "http_archive("; then bad "BAZEL $key@$ver: no http_archive snippet on version page"; return; fi
  # claims rendered into the snippet; page_version html-escapes (e.g. " -> &#34;, + -> &#43;), decode first
  local snipInteg snipStrip page
  page=$(echo "$html" | sed 's/&#34;/"/g; s/&#43;/+/g; s/&#47;/\//g; s/&amp;/\&/g')
  snipInteg=$(echo "$page" | grep -oE 'sha256-[A-Za-z0-9+/]+=*' | head -1)
  snipStrip=$(echo "$page" | grep -oE 'strip_prefix = "[^"]+"' | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
  # served bytes -> recompute integrity exactly as http_archive checks it
  local tgz="/work/bazel-${key//\//-}.tgz"; cd /work
  if ! curl -fsS -m30 "$url/$key/$ver.tar.gz" -o "$tgz"; then bad "BAZEL $key@$ver: tar.gz download failed"; return; fi
  local calcInteg="sha256-$(openssl dgst -sha256 -binary "$tgz" | base64 -w0)"
  if [ -n "$snipInteg" ] && [ "$snipInteg" = "$calcInteg" ]; then
    ok "BAZEL $key@$ver http_archive integrity matches served .tar.gz ($snipInteg)"
  else bad "BAZEL $key@$ver integrity mismatch: snippet=$snipInteg served=$calcInteg"; fi
  # strip_prefix must cover the whole tree (else the http_archive build would not find files)
  if [ -n "$snipStrip" ] && [ "$(tar tzf "$tgz" | grep -c .)" -gt 0 ] \
     && tar tzf "$tgz" | awk -v p="$snipStrip" 'index($0,p)!=1{exit 1}'; then
    ok "BAZEL $key@$ver strip_prefix '$snipStrip' covers the whole archive"
  else bad "BAZEL $key@$ver strip_prefix '$snipStrip' does not match archive top dir"; fi
}

check_zig(){ # url key -> really run `zig fetch` on the universal .tar.gz the build.zig.zon .url snippet points to
  local url=$1 key=$2
  log "ZIG $key  via $url"
  if ! wait_key "$url" "$key"; then bad "ZIG $key: key never appeared at $url"; return; fi
  local ver; ver=$(curl -fsS -m5 "$url/$key/@v/list" | head -1)
  local zc="/work/zig-${key//\//-}"; rm -rf "$zc"; mkdir -p "$zc"
  local out rc
  out=$(zig fetch --global-cache-dir "$zc/.cache" "$url/$key/$ver.tar.gz" 2>&1); rc=$?
  if [ "$rc" -eq 0 ] && [ -n "$out" ]; then
    ok "ZIG $key@$ver fetched+hashed by zig $(zig version) -> ${out%%$'\n'*}"
  else bad "ZIG $key@$ver zig fetch failed (rc=$rc): $(echo "$out" | tr '\n' ' ' | head -c200)"; fi
}

check_converged(){ # src_url dst_url key -> brother must serve the SAME sorted @v/list as its source.
  # Guards against the silent-partial-sync trap: a brother stuck on one old version still answers
  # wait_key (and head -1 picks whatever it has), so the go checks alone PASS while replication
  # is live-locked on newer versions. Window: brothers rescan every 1m -> 36x10s is generous.
  local src=$1 dst=$2 key=$3 tries=${4:-36} want="" got=""
  log "SYNC $key  $dst vs $src"
  for i in $(seq 1 "$tries"); do
    want=$(curl -fsS -m5 "$src/$key/@v/list" 2>/dev/null | sort)
    got=$(curl -fsS -m5 "$dst/$key/@v/list" 2>/dev/null | sort)
    if [ -n "$want" ] && [ "$want" = "$got" ]; then
      ok "SYNC $key: $dst serves all $(echo "$want" | grep -c .) versions of $src"; return; fi
    sleep 10
  done
  bad "SYNC $key: dst=[$(echo $got)] != src=[$(echo $want)] after $((tries*10))s"
}

check_converged_p2(){ # src_url dst_url pkg -> composer /p2 version sets must match on the brother
  local src=$1 dst=$2 pkg=$3 tries=${4:-36} want="" got=""
  log "SYNC(p2) $pkg  $dst vs $src"
  for i in $(seq 1 "$tries"); do
    want=$(curl -fsS -m5 "$src/p2/$pkg.json" 2>/dev/null | jq -r --arg p "$pkg" '[.packages[$p][].version]|sort|join(",")' 2>/dev/null)
    got=$(curl -fsS -m5 "$dst/p2/$pkg.json" 2>/dev/null | jq -r --arg p "$pkg" '[.packages[$p][].version]|sort|join(",")' 2>/dev/null)
    if [ -n "$want" ] && [ "$want" = "$got" ]; then ok "SYNC(p2) $pkg: $dst matches $src ($want)"; return; fi
    sleep 10
  done
  bad "SYNC(p2) $pkg: dst=[$got] != src=[$want] after $((tries*10))s"
}

log "=== verifier start $(date -u +%FT%TZ) ==="
check_rpc_web  "node-a:8080" "node-a seed"
check_rpc_web  "node-c:8080" "node-c brother"
# Go module — seed (git) and brother chain (replicated over ygg across two hubs); all serve vault.test/errors
check_go       "http://node-a:8080" "errors"          # git seed                       (hub-1)
check_go       "http://node-c:8080" "errors"          # brother of node-a (cross-hub)  (hub-2)
check_go       "http://node-e:8080" "errors"          # brother-of-brother (cross-hub) (hub-1)
check_rpc_web_data "node-a:8080" "node-a seed" "errors"
check_rpc_web_data "node-c:8080" "node-c brother" "errors"
# Composer — seed (git) and brother (replicated over ygg)
check_composer "http://node-b:8080" "monolog/monolog" "monolog"  # git seed                      (hub-1)
check_composer "http://node-d:8080" "monolog/monolog" "monolog"  # brother of node-b (cross-hub) (hub-2)
# Bazel + Zig universal snippets — verified against the served universal .tar.gz (Go seed node-a)
check_bazel    "http://node-a:8080" "errors"
check_zig      "http://node-a:8080" "errors"
# Brother convergence — full version-set equality, not just "some version appeared"
check_converged    "http://node-a:8080" "http://node-c:8080" "errors"          # seed -> brother
check_converged    "http://node-c:8080" "http://node-e:8080" "errors"          # brother -> brother-of-brother
check_converged_p2 "http://node-b:8080" "http://node-d:8080" "monolog/monolog" # seed -> brother (composer)
check_converged_p2 "http://node-b:8080" "http://node-d:8080" "psr/log"
# Canonical https through the TLS edge: real go get + real composer dist download over vault.test
check_go_tls
check_composer_tls

log "=== verifier done: PASS=$PASS FAIL=$FAIL ==="
[ "$FAIL" -eq 0 ]
