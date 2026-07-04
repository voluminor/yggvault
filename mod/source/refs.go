package source

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// // // // // // // // // //

const (
	// cRefsMaxBytes caps refs advertisements; large monorepos still fit in a few megabytes.
	cRefsMaxBytes = 16 << 20

	// cRefsService is the smart-HTTP v0 service name.
	cRefsService = "git-upload-pack"

	cTagRefPrefix = "refs/tags/"
	cPeeledSuffix = "^{}"
)

// //

var errRefsTooLarge = errors.New("git refs advertisement exceeds size limit")

// // // // // // // // // //

func refsAdvertisementURL(sourceURL string) (string, error) {
	parsedObj, err := url.Parse(strings.TrimSpace(sourceURL))
	if err != nil {
		return "", fmt.Errorf("parse source url: %w", err)
	}
	if parsedObj.Scheme != "http" && parsedObj.Scheme != "https" {
		return "", fmt.Errorf("unsupported source url scheme %q", parsedObj.Scheme)
	}
	if parsedObj.Host == "" {
		return "", errors.New("source url has no host")
	}
	parsedObj.Path = strings.TrimRight(parsedObj.Path, "/") + "/info/refs"
	parsedObj.RawQuery = "service=" + cRefsService
	parsedObj.Fragment = ""
	return parsedObj.String(), nil
}

func isHexSHA(text string) bool {
	if len(text) != 40 && len(text) != 64 {
		return false
	}
	for i := 0; i < len(text); i++ {
		symbolByte := text[i]
		if (symbolByte < '0' || symbolByte > '9') && (symbolByte < 'a' || symbolByte > 'f') && (symbolByte < 'A' || symbolByte > 'F') {
			return false
		}
	}
	return true
}

func trimPktNewline(payloadArr []byte) []byte {
	if len(payloadArr) > 0 && payloadArr[len(payloadArr)-1] == '\n' {
		return payloadArr[:len(payloadArr)-1]
	}
	return payloadArr
}

// readPktLine reads one pkt-line: 4 hex length bytes including the header, then payload.
// The second result reports flush-pkt `0000`.
func readPktLine(bufObj *bufio.Reader) ([]byte, bool, error) {
	headArr := make([]byte, 4)
	if _, err := io.ReadFull(bufObj, headArr); err != nil {
		return nil, false, err
	}
	lengthValue, err := strconv.ParseUint(string(headArr), 16, 32)
	if err != nil {
		return nil, false, fmt.Errorf("invalid pkt-line length %q", string(headArr))
	}
	if lengthValue == 0 {
		return nil, true, nil
	}
	if lengthValue < 4 {
		return nil, false, fmt.Errorf("reserved pkt-line length %d", lengthValue)
	}
	payloadArr := make([]byte, lengthValue-4)
	if _, err = io.ReadFull(bufObj, payloadArr); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return nil, false, err
	}
	return payloadArr, false, nil
}

// parseRefsAdvertisement parses v0 service header, flush, and `<sha> <refname>[\0caps]` lines.
// Only refs/tags/* are collected; peeled refs replace annotated-tag object SHAs with commit SHAs.
func parseRefsAdvertisement(readerObj io.Reader, maxBytes int64) (map[string]string, error) {
	limitedObj := &io.LimitedReader{R: readerObj, N: maxBytes + 1}
	bufObj := bufio.NewReader(limitedObj)

	wrapEOF := func(err error) error {
		if limitedObj.N <= 0 && (errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)) {
			return errRefsTooLarge
		}
		return err
	}

	payloadArr, flushFlag, err := readPktLine(bufObj)
	if err != nil {
		return nil, wrapEOF(err)
	}
	if flushFlag || string(trimPktNewline(payloadArr)) != "# service="+cRefsService {
		return nil, errors.New("unexpected refs advertisement header")
	}

	tagsObj := make(map[string]string)
	peeledObj := make(map[string]struct{})
	for {
		payloadArr, flushFlag, err = readPktLine(bufObj)
		if errors.Is(err, io.EOF) {
			// EOF at the byte limit means truncation exactly on a pkt-line boundary.
			if limitedObj.N <= 0 {
				return nil, errRefsTooLarge
			}
			break
		}
		if err != nil {
			return nil, wrapEOF(err)
		}
		if flushFlag {
			continue
		}
		lineText := string(trimPktNewline(payloadArr))
		// Capabilities appear after NUL only on the first ref line.
		if capIdx := strings.IndexByte(lineText, 0); capIdx >= 0 {
			lineText = lineText[:capIdx]
		}
		shaText, refName, okFlag := strings.Cut(lineText, " ")
		if !okFlag || !isHexSHA(shaText) {
			return nil, fmt.Errorf("malformed ref line %q", lineText)
		}
		if !strings.HasPrefix(refName, cTagRefPrefix) {
			continue
		}
		tagName := refName[len(cTagRefPrefix):]
		if strings.HasSuffix(tagName, cPeeledSuffix) {
			tagName = strings.TrimSuffix(tagName, cPeeledSuffix)
			if tagName == "" {
				continue
			}
			tagsObj[tagName] = strings.ToLower(shaText)
			peeledObj[tagName] = struct{}{}
			continue
		}
		if _, peeledFlag := peeledObj[tagName]; peeledFlag {
			continue
		}
		tagsObj[tagName] = strings.ToLower(shaText)
	}
	return tagsObj, nil
}

// // // // // // // // // //

// Refs returns tag-to-commit SHA from a smart-HTTP refs advertisement, like `git ls-remote --tags`.
// It uses one anonymous GET and works across common git forges without provider dispatch.
func (obj *Obj) Refs(ctx context.Context, sourceURL string) (map[string]string, error) {
	refsURL, err := refsAdvertisementURL(sourceURL)
	if err != nil {
		return nil, permanent(err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, obj.requestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, refsURL, nil)
	if err != nil {
		return nil, permanent(err)
	}
	req.Header.Set("User-Agent", cUserAgent)

	resp, err := obj.refsClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("git refs %q: http status %d", refsURL, resp.StatusCode)
	}

	tagsObj, err := parseRefsAdvertisement(resp.Body, cRefsMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("git refs %q: %w", refsURL, err)
	}
	return tagsObj, nil
}
