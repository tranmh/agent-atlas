// Package httpx holds the small shared platform used by every Go service in
// this repo: the JSON error shape, request logging + CORS middleware, env
// helpers, Server-Sent Events plumbing (SSEWriter + generic Broadcaster) and
// the container healthcheck helper. Copied from cp_amap_pci (Postgres parts
// dropped).
package httpx
