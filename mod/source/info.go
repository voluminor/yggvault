package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/voluminor/yggvault/mod/route"
)

// // // // // // // // // //

const cInfoMaxBytes = 64 << 10

// // // // // // // // // //

type brotherInfoObj struct {
	Domain      string  `json:"domain"`
	YggHost     string  `json:"ygg_host"`
	RoutePrefix *string `json:"route_prefix"`
}

func (obj *Obj) fetchBrotherInfo(ctx context.Context, rootURL string) (webAddr string, yggAddr string, routePrefix *string, ok bool) {
	infoURL, err := instanceURLFor(rootURL, route.Info)
	if err != nil {
		return "", "", nil, false
	}
	body, gotBody := obj.getLimitedBody(ctx, infoURL, cInfoMaxBytes)
	if !gotBody {
		return "", "", nil, false
	}
	var infoObj brotherInfoObj
	if json.Unmarshal(body, &infoObj) != nil {
		return "", "", nil, false
	}
	return infoObj.Domain, infoObj.YggHost, infoObj.RoutePrefix, true
}

func (obj *Obj) checkAddrDivergence(rootURL string, webAddr string, yggAddr string) error {
	u, err := url.Parse(rootURL)
	if err != nil {
		return err
	}
	host := u.Hostname()
	if obj.isMeshHost(host) {
		if yggAddr == "" || !strings.EqualFold(host, yggAddr) {
			return fmt.Errorf("config ygg host %q not advertised by /info (ygg_host=%q)", host, yggAddr)
		}
		return nil
	}
	if webAddr == "" || !strings.EqualFold(host, webAddr) {
		return fmt.Errorf("config web host %q not advertised by /info (domain=%q)", host, webAddr)
	}
	return nil
}
