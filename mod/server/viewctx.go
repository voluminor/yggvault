package server

import (
	"sort"

	"github.com/voluminor/yggvault/mod/route"
	"github.com/voluminor/yggvault/mod/view"
	"github.com/voluminor/yggvault/target"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	cViewService = "yggvault"
	cViewTagline = "content-addressed release vault"
)

// // // // // // // // // //

func (obj *funcObj) webPublicScheme() string {
	serverObj := obj.deps.Config.Web.Server
	if serverObj.Mode == stconf.WebServerModeSingle && serverObj.Single.Proto == stconf.WebProtoHttp {
		return "http"
	}
	return "https"
}

func (obj *funcObj) alternateListenerCtx(lc listenerCtxObj) (listenerCtxObj, bool) {
	switch lc.listenerID {
	case stcode.ListenerWeb:
		if obj.deps.Mesh == nil || !obj.deps.Mesh.Enabled() {
			return listenerCtxObj{}, false
		}
		host := obj.deps.Mesh.Host()
		if host == "" {
			if ipObj := obj.deps.Mesh.Address(); ipObj != nil {
				host = "[" + ipObj.String() + "]"
			}
		}
		if host == "" {
			return listenerCtxObj{}, false
		}
		return listenerCtxObj{listenerID: stcode.ListenerYgg, entryHost: host, secure: false}, true
	case stcode.ListenerYgg:
		domain := obj.deps.Config.Web.Server.Domain
		if domain == "" {
			return listenerCtxObj{}, false
		}
		return listenerCtxObj{listenerID: stcode.ListenerWeb, entryHost: domain, secure: obj.webPublicScheme() == "https"}, true
	default:
		return listenerCtxObj{}, false
	}
}

func (obj *funcObj) alternateEntry(lc listenerCtxObj) view.AlternateObj {
	altLC, ok := obj.alternateListenerCtx(lc)
	if !ok {
		return view.AlternateObj{}
	}
	if altLC.listenerID == stcode.ListenerYgg {
		linkHost := altLC.entryHost
		if ipObj := obj.deps.Mesh.Address(); ipObj != nil {
			linkHost = "[" + ipObj.String() + "]"
		}
		return view.AlternateObj{Channel: "ygg", Scheme: altLC.scheme(), Host: linkHost, CopyHost: altLC.entryHost}
	}
	return view.AlternateObj{Channel: "web", Scheme: altLC.scheme(), Host: altLC.entryHost, CopyHost: altLC.entryHost}
}

// // // // // // // // // //

func sortedContactGroups(contactMap map[string][]string) []view.ContactGroupObj {
	groupArr := make([]view.ContactGroupObj, 0, len(contactMap))
	for name, valueArr := range contactMap {
		groupArr = append(groupArr, view.ContactGroupObj{Name: name, Values: valueArr})
	}
	sort.Slice(groupArr, func(i, j int) bool { return groupArr[i].Name < groupArr[j].Name })
	return groupArr
}

func (obj *funcObj) viewService(homePath string) view.ServiceObj {
	infoObj := obj.deps.Config.Info
	return view.ServiceObj{
		Name:         cViewService,
		Tagline:      cViewTagline,
		HomeURL:      homePath,
		InfoName:     infoObj.Name,
		Description:  infoObj.Description,
		Location:     infoObj.Location,
		Contacts:     obj.contactGroups,
		BuildVersion: target.Version,
		BuildDate:    target.DateUpdate,
	}
}

func (obj *funcObj) viewContext(lc listenerCtxObj) view.ContextObj {
	lnk := obj.linkCtx(lc)
	homePath := lnk.Key("", "")
	var navArr []view.ActionObj
	if homePath != "/" {
		navArr = append(navArr, view.ActionObj{Label: "Home", URL: "/"})
	}
	navArr = append(navArr, view.ActionObj{Label: "Packages", URL: homePath})
	if lc.publicMetricsEnabled || lc.internalMetricsEnabled {
		navArr = append(navArr, view.ActionObj{Label: "Metrics", URL: route.Metrics})
	}
	return view.ContextObj{
		Service: obj.viewService(homePath),
		Client: view.ClientObj{
			Channel: lc.listenerID.String(),
			Scheme:  lc.scheme(),
			Host:    lc.entryHost,
		},
		Alternate:  obj.alternateEntry(lc),
		Navigation: navArr,
	}
}
