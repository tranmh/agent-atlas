/** Base URLs of the running stack (`make up`); override with E2E_* env vars. */
export const WEB_URL = process.env.E2E_WEB_URL ?? "http://localhost:3000";
export const VIZ_URL = process.env.E2E_VISUALIZER_URL ?? "http://localhost:8095";
export const GATEWAY_URL = process.env.E2E_GATEWAY_URL ?? "http://localhost:8000";
export const JAEGER_URL = process.env.E2E_JAEGER_URL ?? "http://localhost:16686";
