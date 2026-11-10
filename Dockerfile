# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && \
    addgroup -S -g 10001 sitetrack && \
    adduser -S -D -H -u 10001 -G sitetrack sitetrack

WORKDIR /app
COPY --from=build /out/api /out/migrate /app/
USER sitetrack
EXPOSE 8080
CMD ["/app/api"]
