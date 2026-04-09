# Build stage
FROM golang:1.21-alpine AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o gobivc cmd/server/main.go

# Runtime stage
FROM alpine:3.19

RUN apk add --no-cache ca-certificates sqlite-libs

WORKDIR /app
COPY --from=builder /app/gobivc .
COPY --from=builder /app/web ./web

RUN mkdir -p /app/data

ENV PORT=8080
ENV DATA_DIR=/app/data

EXPOSE 8080

CMD ["./gobivc"]
