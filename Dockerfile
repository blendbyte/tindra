# Stage 1: Build frontend (runs on build host for speed)
# Base images are pinned by digest; Renovate updates tag and digest together.
# Keep the Bun version in step with packageManager in web/package.json.
FROM --platform=$BUILDPLATFORM oven/bun:1.4.3-alpine@sha256:629e17411f1f129dbec3af78d5af9c9f2a937435c80349437206c6b0b7422373 AS web-builder
WORKDIR /web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
# Only bundle here. vue-tsc needs Node to type-check .vue files, and this image
# runs scripts under Bun; the required verify workflow type-checks every
# revision before an image is published.
RUN bunx vite build

# Stage 2: Compile Go binary (cross-compile on build host)
FROM --platform=$BUILDPLATFORM golang:1.27.2-alpine@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /internal/ui/dist ./internal/ui/dist
ARG TARGETOS TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT}" \
    -o /tindra ./cmd/tindra

RUN mkdir -p /data/sourcemaps

# Stage 3: Minimal runtime image
FROM gcr.io/distroless/static-debian13:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
COPY --from=builder /tindra /tindra
COPY --from=builder --chown=65532:65532 /data/ /data/
EXPOSE 8080
# The image has no shell or curl; the binary checks its own /healthz.
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 CMD ["/tindra", "healthcheck"]
ENTRYPOINT ["/tindra", "serve"]
