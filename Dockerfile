# Stage 1: Build frontend (runs on build host for speed)
# Base images are pinned by digest; Renovate updates tag and digest together.
# Keep the Bun version in step with packageManager in web/package.json.
FROM --platform=$BUILDPLATFORM oven/bun:1.4.4-alpine@sha256:4cf82f5686b196504fe119db3fcd9185b191f82ece82c40680e597228ec6070a AS web-builder
WORKDIR /web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
# Only bundle here. vue-tsc needs Node to type-check .vue files, and this image
# runs scripts under Bun; the required verify workflow type-checks every
# revision before an image is published.
RUN bunx vite build

# Stage 2: Compile Go binary (cross-compile on build host)
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS builder
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
FROM gcr.io/distroless/static-debian13:nonroot@sha256:aafe993665bf6200db38a02c11a9351e782a84b6a45e7f33b6695638c88e3931
COPY --from=builder /tindra /tindra
COPY --from=builder --chown=65532:65532 /data/ /data/
EXPOSE 8080
# The image has no shell or curl; the binary checks its own /healthz.
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 CMD ["/tindra", "healthcheck"]
ENTRYPOINT ["/tindra", "serve"]
