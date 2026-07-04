package webui

import (
	"encoding/base64"
	"strconv"
	"strings"
)

// // // // // // // // // //

// PageCursorObj addresses one keyset page of the key version list.
// The zero value (Set=false) selects the newest page.
type PageCursorObj struct {
	Set     bool   // false → newest page (no cursor in the URL)
	Newer   bool   // true → versions newer than (Seq,Version); false → older
	Seq     int64  // upstream_seq half of the keyset cursor
	Version string // version half of the keyset cursor
}

// // // // // // // // // //

// AfterCursor builds the "older" input cursor from a decoded token.
func AfterCursor(seq int64, version string) PageCursorObj {
	return PageCursorObj{Set: true, Newer: false, Seq: seq, Version: version}
}

// BeforeCursor builds the "newer" input cursor from a decoded token.
func BeforeCursor(seq int64, version string) PageCursorObj {
	return PageCursorObj{Set: true, Newer: true, Seq: seq, Version: version}
}

// // // // // // // // // //

// EncodeCursor packs a (upstream_seq, version) keyset position into an opaque URL-safe token.
func EncodeCursor(seq int64, version string) string {
	raw := strconv.FormatInt(seq, 10) + "\x00" + version
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor reverses EncodeCursor. ok=false rejects a malformed token so the caller falls back to
// the newest page instead of trusting attacker-supplied cursor bytes.
func DecodeCursor(token string) (seq int64, version string, ok bool) {
	rawArr, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, "", false
	}
	raw := string(rawArr)
	sepIdx := strings.IndexByte(raw, 0)
	if sepIdx < 0 {
		return 0, "", false
	}
	seq, err = strconv.ParseInt(raw[:sepIdx], 10, 64)
	if err != nil {
		return 0, "", false
	}
	return seq, raw[sepIdx+1:], true
}
