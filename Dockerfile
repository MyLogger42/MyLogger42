# syntax=docker/dockerfile:1.7

# Multi-stage build: node for the SPA, golang for the binary, alpine
# for the runtime. The final image carries only the Go binary and a
# data directory; SPA assets are embedded into the binary.

# ---------- Stage 1: build the SPA ----------
FROM node:20-alpine AS web-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json* ./
# package-lock may not exist on first build; the wildcard above means
# the COPY won't fail. npm install handles either case.
RUN npm install
COPY web/ ./
RUN npm run build

# ---------- Stage 2: build the Go binary ----------
FROM golang:1.22-alpine AS go-builder
WORKDIR /src
RUN apk add --no-cache git
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
# Drop the freshly built SPA into web/dist where the embed directive
# will pick it up. We replace whatever the COPY above brought in.
COPY --from=web-builder /src/web/dist ./web/dist
# CGO is off — we use modernc.org/sqlite which is pure Go, so the
# resulting binary is fully static and works on scratch/distroless.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' \
    -o /out/mylogger42 ./cmd/server

# ---------- Stage 3: runtime ----------
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=go-builder /out/mylogger42 /app/mylogger42
RUN mkdir -p /app/data && chown -R app:app /app
USER app
ENV DB_DSN=/app/data/mylogger42.db \
    HTTP_PORT=8080
EXPOSE 8080
VOLUME ["/app/data"]
ENTRYPOINT ["/app/mylogger42"]
