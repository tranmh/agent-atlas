# syntax=docker/dockerfile:1
# Next.js standalone build of the Agent Atlas web app (build context: web/).

# ---- deps ----
FROM node:22-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci

# ---- builder ----
FROM node:22-alpine AS builder
WORKDIR /app
# NEXT_PUBLIC_* are inlined into the browser bundle at build time.
ARG NEXT_PUBLIC_VISUALIZER_URL=http://localhost:8095
ARG NEXT_PUBLIC_GATEWAY_URL=http://localhost:8000
ARG NEXT_PUBLIC_JAEGER_URL=http://localhost:16686
ARG NEXT_PUBLIC_GATEWAY_UI_URL=http://localhost:15000/ui
# "1" = replay the bundled sample without a backend.
ARG NEXT_PUBLIC_USE_FIXTURES=
ENV NEXT_PUBLIC_VISUALIZER_URL=$NEXT_PUBLIC_VISUALIZER_URL \
    NEXT_PUBLIC_GATEWAY_URL=$NEXT_PUBLIC_GATEWAY_URL \
    NEXT_PUBLIC_JAEGER_URL=$NEXT_PUBLIC_JAEGER_URL \
    NEXT_PUBLIC_GATEWAY_UI_URL=$NEXT_PUBLIC_GATEWAY_UI_URL \
    NEXT_PUBLIC_USE_FIXTURES=$NEXT_PUBLIC_USE_FIXTURES \
    NEXT_TELEMETRY_DISABLED=1
COPY --from=deps /app/node_modules ./node_modules
COPY . .
RUN npm run build

# ---- runner ----
FROM node:22-alpine AS runner
WORKDIR /app
ENV NODE_ENV=production \
    NEXT_TELEMETRY_DISABLED=1 \
    PORT=3000 \
    HOSTNAME=0.0.0.0

# busybox wget is enough for the compose healthcheck (GET /api/health).
RUN addgroup --system --gid 1001 nodejs \
  && adduser --system --uid 1001 nextjs

COPY --from=builder --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=builder --chown=nextjs:nodejs /app/.next/static ./.next/static
COPY --from=builder --chown=nextjs:nodejs /app/public ./public

USER nextjs
EXPOSE 3000

HEALTHCHECK --interval=10s --timeout=5s --retries=6 --start-period=20s \
  CMD wget -q -O - http://127.0.0.1:3000/api/health || exit 1

CMD ["node", "server.js"]
