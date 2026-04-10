# syntax=docker/dockerfile:1
# Multi-arch pure-Go build (no CGO), suitable for any cloud platform.

FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -ldflags="-s -w" -o gobivc cmd/server/main.go

# Minimal runtime image
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata && \
    adduser -D -H -u 1000 gobivc

WORKDIR /app
COPY --from=builder /app/gobivc .
COPY --from=builder /app/web ./web

RUN mkdir -p /app/data && chown -R gobivc:gobivc /app

USER gobivc

ENV PORT=8080
ENV DATA_DIR=/app/data
ENV TZ=Asia/Shanghai

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1

ENTRYPOINT ["./gobivc"]
