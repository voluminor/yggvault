package source

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// // // // // // // // // //

const cRefsServiceHeader = "# service=git-upload-pack\n"

// //

func pktLine(payload string) string {
	return fmt.Sprintf("%04x", len(payload)+4) + payload
}

func testSHA(symbolByte byte) string {
	return strings.Repeat(string(symbolByte), 40)
}

func refsBody(refLineArr ...string) string {
	builderObj := strings.Builder{}
	builderObj.WriteString(pktLine(cRefsServiceHeader))
	builderObj.WriteString("0000")
	for _, lineText := range refLineArr {
		builderObj.WriteString(pktLine(lineText))
	}
	builderObj.WriteString("0000")
	return builderObj.String()
}

// // // // // // // // // //

func TestRefsCollectsTagsAndPrefersPeeled(t *testing.T) {
	shaHead := testSHA('0')
	shaBranch := testSHA('1')
	shaTagObject := testSHA('2')
	shaPeeled := testSHA('3')
	shaLightweight := testSHA('4')

	bodyText := refsBody(
		shaHead+" HEAD\x00multi_ack side-band-64k symref=HEAD:refs/heads/main\n",
		shaBranch+" refs/heads/main\n",
		shaTagObject+" refs/tags/v1.0.0\n",
		shaPeeled+" refs/tags/v1.0.0^{}\n",
		shaLightweight+" refs/tags/v2.0.0\n",
	)

	tsObj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/owner/repo/info/refs" {
			t.Errorf("unexpected path %q", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("service") != "git-upload-pack" {
			t.Errorf("unexpected query %q", r.URL.RawQuery)
		}
		_, _ = w.Write([]byte(bodyText))
	}))
	defer tsObj.Close()

	obj := newTestObj(t, testConfigObj(t))
	tagsObj, err := obj.Refs(context.Background(), tsObj.URL+"/owner/repo/")
	if err != nil {
		t.Fatalf("Refs returned error: %v", err)
	}
	if len(tagsObj) != 2 {
		t.Fatalf("got %d tags, want 2: %v", len(tagsObj), tagsObj)
	}
	if tagsObj["v1.0.0"] != shaPeeled {
		t.Fatalf("v1.0.0 sha=%q, want peeled %q", tagsObj["v1.0.0"], shaPeeled)
	}
	if tagsObj["v2.0.0"] != shaLightweight {
		t.Fatalf("v2.0.0 sha=%q, want %q", tagsObj["v2.0.0"], shaLightweight)
	}
}

func TestRefsParsePeeledBeforeTagObjectStillWins(t *testing.T) {
	shaTagObject := testSHA('a')
	shaPeeled := testSHA('b')
	bodyText := refsBody(
		shaPeeled+" refs/tags/v1.0.0^{}\n",
		shaTagObject+" refs/tags/v1.0.0\n",
	)
	tagsObj, err := parseRefsAdvertisement(strings.NewReader(bodyText), cRefsMaxBytes)
	if err != nil {
		t.Fatalf("parse returned error: %v", err)
	}
	if tagsObj["v1.0.0"] != shaPeeled {
		t.Fatalf("v1.0.0 sha=%q, want peeled %q regardless of line order", tagsObj["v1.0.0"], shaPeeled)
	}
}

func TestRefsParseSizeCap(t *testing.T) {
	bodyText := refsBody(
		testSHA('c')+" refs/tags/v1.0.0\n",
		testSHA('d')+" refs/tags/v2.0.0\n",
	)
	_, err := parseRefsAdvertisement(strings.NewReader(bodyText), 48)
	if !errors.Is(err, errRefsTooLarge) {
		t.Fatalf("got %v, want errRefsTooLarge", err)
	}
}

func TestRefsParseRejectsBadHeaderAndBadLine(t *testing.T) {
	badHeader := pktLine("# service=git-receive-pack\n") + "0000"
	if _, err := parseRefsAdvertisement(strings.NewReader(badHeader), cRefsMaxBytes); err == nil {
		t.Fatal("bad service header must fail")
	}
	badLine := refsBody("not-a-sha refs/tags/v1.0.0\n")
	if _, err := parseRefsAdvertisement(strings.NewReader(badLine), cRefsMaxBytes); err == nil {
		t.Fatal("malformed ref sha must fail")
	}
	badLength := pktLine(cRefsServiceHeader) + "0000" + "zzzz"
	if _, err := parseRefsAdvertisement(strings.NewReader(badLength), cRefsMaxBytes); err == nil {
		t.Fatal("invalid pkt-line length must fail")
	}
}

func TestRefsNon200(t *testing.T) {
	tsObj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	}))
	defer tsObj.Close()

	obj := newTestObj(t, testConfigObj(t))
	if _, err := obj.Refs(context.Background(), tsObj.URL+"/owner/repo"); err == nil {
		t.Fatal("non-200 must fail")
	}
}

func TestRefsRejectsBadURL(t *testing.T) {
	obj := newTestObj(t, testConfigObj(t))
	if _, err := obj.Refs(context.Background(), "ftp://example.com/repo"); err == nil {
		t.Fatal("non-http scheme must fail")
	}
}

// refs runs anonymously: host credentials must not reach git endpoints because GitHub rejects Bearer
// even for public repositories with valid tokens.
func TestRefsSendsNoCredentials(t *testing.T) {
	gotAuthCh := make(chan string, 1)
	bodyText := refsBody(testSHA('a') + " refs/tags/v1.0.0\x00caps\n")
	tsObj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case gotAuthCh <- r.Header.Get("Authorization"):
		default:
		}
		_, _ = w.Write([]byte(bodyText))
	}))
	defer tsObj.Close()

	configObj := testConfigObj(t)
	configObj.Source.Credentials.Others = map[string]string{"127.0.0.1": "Authorization: Bearer test-secret"}
	obj := newTestObj(t, configObj)

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, tsObj.URL+"/probe", nil)
	resp, err := obj.metaClient.Do(req)
	if err != nil {
		t.Fatalf("metaClient probe: %v", err)
	}
	_ = resp.Body.Close()
	if got := <-gotAuthCh; got != "Bearer test-secret" {
		t.Fatalf("meta client must attach credentials, got %q", got)
	}

	if _, err := obj.Refs(context.Background(), tsObj.URL+"/owner/repo"); err != nil {
		t.Fatalf("Refs: %v", err)
	}
	if got := <-gotAuthCh; got != "" {
		t.Fatalf("refs request leaked credentials: Authorization=%q", got)
	}
}
