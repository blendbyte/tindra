# Stage 1: Build frontend (runs on build host for speed)
FROM --platform=$BUILDPLATFORM oven/bun:latest AS web-builder
WORKDIR /web
COPY web/package.json web/bun.lock ./
RUN bun install --frozen-lockfile
COPY web/ ./
RUN bun run build

# Stage 2: Compile Go binary (cross-compile on build host)
FROM --platform=$BUILDPLATFORM golang:1.26.3-alpine AS builder
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

# Stage 3: Minimal runtime image
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /tindra /tindra
EXPOSE 8080
ENTRYPOINT ["/tindra", "serve"]
