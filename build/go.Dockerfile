# syntax=docker/dockerfile:1
# One parameterised image for every Go service:
#   docker build -f build/go.Dockerfile --build-arg SERVICE=agent .
# SERVICE is one of: agent | orchestrator | visualizer | mcpserver (a directory under cmd/).
ARG SERVICE=agent

FROM golang:1.26-alpine AS build
ARG SERVICE
WORKDIR /src

# Cache module downloads in their own layer (only invalidated by go.mod/go.sum).
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

# Copy the source and build a static binary. The build cache mount makes the
# service builds share compiled packages.
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/${SERVICE} ./cmd/${SERVICE}

# Runtime: alpine (not scratch) so the binary can run its own healthcheck
# (/app/<service> --healthcheck) and there is a shell for debugging.
FROM alpine:3.22
ARG SERVICE
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/${SERVICE} /app/${SERVICE}
# ARGs are not visible to an exec-form ENTRYPOINT, so expose the binary under a
# fixed name as well; signals still reach the Go process directly (no shell).
RUN ln -s /app/${SERVICE} /app/service
ENV SERVICE=${SERVICE}
USER app
ENTRYPOINT ["/app/service"]
