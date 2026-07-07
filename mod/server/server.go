package server

import (
	"context"
	"crypto/tls"

	"github.com/rs/zerolog"

	"github.com/voluminor/yggvault/mod/mesh"
	"github.com/voluminor/yggvault/mod/server/brother"
	"github.com/voluminor/yggvault/mod/server/static"
	"github.com/voluminor/yggvault/target/api"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

// ServerObj holds the edge server.
// A single ogen router serves per-listener http.Server instances on web and Yggdrasil sockets.
// Routing and response encoding stay in the router; listeners and lifecycle live here and in listener.go.
type ServerObj struct {
	cfg         *stconf.ConfigObj
	router      *api.Server
	meshObj     mesh.NodeInterface
	tlsConfig   *tls.Config
	logObj      zerolog.Logger
	listenerArr []*listenerObj
	brotherObj  *brother.ServerObj
	staticSnap  *static.SnapshotObj // nil means static serving is disabled.
	funcImplObj *funcObj            // shared source of host context and pages for HTML not-found responses
	openapiSpec openapiSpecObj      // precomputed OpenAPI spec and ETag
}

// // // // // // // // // //

func loadTLS(certFile string, keyFile string) (*tls.Config, error) {
	if certFile == "" || keyFile == "" {
		return nil, nil
	}
	certObj, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{Certificates: []tls.Certificate{certObj}, MinVersion: tls.VersionTLS12}, nil
}

// // // // // // // // // //

// New assembles the edge server in three steps: funcObj, sub-services, then the ogen router.
// The router receives telemetry, middleware, error handling and not-found pages.
// Listener sockets are bound separately in Start from listener.go.
func New(depsObj DepsObj) (*ServerObj, error) {
	assetSnapshotObj, err := buildAssetSnapshot()
	if err != nil {
		return nil, err
	}
	funcImplObj := &funcObj{
		deps:          depsObj,
		objCache:      newObjCache(),
		assets:        assetSnapshotObj,
		contactGroups: sortedContactGroups(depsObj.Config.Info.Contacts),
	}

	specObj, err := newOpenAPISpec(depsObj.Config)
	if err != nil {
		return nil, err
	}

	tlsConfigObj, err := loadTLS(depsObj.Config.Web.Server.Tls.Cert, depsObj.Config.Web.Server.Tls.Key)
	if err != nil {
		return nil, err
	}

	brotherObj, err := brother.New(depsObj.Config, depsObj.Storage)
	if err != nil {
		return nil, err
	}

	staticSnapObj, err := buildStaticSnapshot(depsObj.Config)
	if err != nil {
		brotherObj.Close()
		return nil, err
	}

	serverObj := &ServerObj{
		cfg:         depsObj.Config,
		meshObj:     depsObj.Mesh,
		tlsConfig:   tlsConfigObj,
		logObj:      depsObj.Log,
		brotherObj:  brotherObj,
		staticSnap:  staticSnapObj,
		funcImplObj: funcImplObj,
		openapiSpec: specObj,
	}

	routerObj, err := api.NewServer(
		&api.HandlerObj{FuncObj: funcImplObj},
		api.WithMeterProvider(depsObj.Telemetry.MeterProvider()),
		api.WithMiddleware(opMiddleware),
		api.WithErrorHandler(errorHandler),
		api.WithNotFound(serverObj.notFound),
	)
	if err != nil {
		brotherObj.Close()
		return nil, err
	}
	serverObj.router = routerObj

	return serverObj, nil
}

func buildStaticSnapshot(cfgObj *stconf.ConfigObj) (*static.SnapshotObj, error) {
	dir := cfgObj.Web.Static.Dir
	if dir == "" {
		return nil, nil
	}
	snapshotObj, err := static.New(dir, cfgObj.Web.Static.IndexFile, uint64(cfgObj.Web.Static.MaxSize), cfgObj.Web.Static.Deny)
	if err != nil {
		return nil, err
	}
	snapshotObj.SetCacheMaxAge(cfgObj.Web.Static.CacheMaxAge)
	return snapshotObj, nil
}

// Shutdown first closes brother sessions, then drains http.Server until the ctx deadline.
func (obj *ServerObj) Shutdown(ctx context.Context) error {
	obj.brotherObj.Close()
	return obj.shutdownListeners(ctx)
}
