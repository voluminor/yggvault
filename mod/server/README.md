# mod/server

`mod/server` is the inbound serving layer. It accepts web and Yggdrasil HTTP traffic, applies ingress limits, routes
generated API calls, serves package-manager endpoints, renders the HTML UI, streams artifacts, exposes metrics, and
hosts brother RPC on enabled web and Yggdrasil entries. The same public release and artifact routes can also be used
by a brother as a read-only fallback when RPC is disabled.

## Place in the Runtime

```mermaid
flowchart TD
  web["web listener"] --> front["front handler"]
  ygg["Yggdrasil listener"] --> front
  front --> rpc["brother RPC gate"]
  front --> router["generated router"]
  router --> funcs["funcObj"]
  funcs --> builders["dataapi, goproxy, composer, feedatom, webui"]
  builders --> storage["storage"]
  builders --> state["state"]
  builders --> cache["byte cache"]
```

## Responsibilities

- Start listeners for `single`, `shared`, `split`, and Yggdrasil modes.
- Add listener context, request id, method gate, URI cap, rate limits, and route-prefix handling.
- Implement generated `api.FuncInterface` handlers.
- Serve JSON APIs, Go proxy routes, Composer routes, Atom feeds, HTML UI, OpenAPI, sitemap, logos, favicon, and OG
  images.
- Stream large artifacts with `ETag`, `Last-Modified`, `Content-Length`, `Range`, and `HEAD` support.
- Gate brother RPC per listener with `brother.rpc.web_enabled` and `brother.rpc.ygg_enabled`.
- Keep public release metadata and universal archives usable for brother fallback even when `/rpc` is disabled.
- Expose metrics JSON groups and optional internal Prometheus text.

## Request Flow

```mermaid
sequenceDiagram
  participant Client as client
  participant Front as front handler
  participant Router as generated router
  participant Handler as domain handler
  participant Store as storage/cache/state
  Client->>Front: HTTP request
  Front->>Front: ingress checks and listener context
  Front->>Router: route request
  Router->>Handler: typed API call
  Handler->>Store: read or build response
  Store-->>Handler: bytes, stream, or typed result
  Handler-->>Client: typed response
```

## Contracts

- Business handlers do not write directly to `http.ResponseWriter`; they return generated response types.
- Artifacts stream from disk or builders, not through unbounded RAM buffers.
- HTML builders must use request context so canceled clients stop expensive storage work.
- RPC is fail-closed when disabled for the current listener.
- `/rpc` accepts only `CONNECT`; normal HTTP methods do not enter the RPC server.
- Public fallback uses normal generated routes, so reverse proxies handle it like ordinary read-only client traffic.
- Metrics labels must stay low-cardinality.

## Important Files

- `server.go`, `deps.go`, `listener.go`: assembly and lifecycle.
- `front.go`, `hooks.go`, `respond.go`, `errors.go`: common HTTP frame.
- `funcimpl*.go`: generated API handler implementation.
- `artifact.go`, `objcache.go`, `metrics.go`, `assets.go`: artifact serving, response cache, metrics, built-in assets.
- `brother/`: RPC server.
- `dataapi/`, `goproxy/`, `composer/`, `feedatom/`, `webui/`: domain response builders.

## Operational Notes

`shared` mode is for a TLS-terminating reverse proxy where the public scheme is HTTPS but the process listens on one
plain HTTP socket. `split` mode is for separate HTTP and HTTPS binds. `single` mode is simplest for local development.
Brother RPC is mounted before the generated API router, so the OpenAPI route list does not describe it. Public fallback
uses generated API and artifact routes and therefore follows the same prefix, cache, and proxy behavior as normal
clients.

In nested mode (`web.static.dir` set) per-key API routes move under `web.routing.prefix`, but service routes stay at
their well-known root paths. `/info` therefore stays reachable at the root and advertises `route_prefix`: empty when the
API is served at the root, otherwise the mounted segment. A brother reads this field during discovery to derive remote
keys under the prefix the node actually serves.
