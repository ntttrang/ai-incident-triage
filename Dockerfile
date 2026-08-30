# syntax=docker/dockerfile:1

FROM golang:1.26-alpine AS builder
WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/worker ./cmd/worker

FROM alpine:3.21
WORKDIR /app

RUN apk add --no-cache ca-certificates wget \
  && adduser -D -u 65532 nonroot

COPY --from=builder /out/api /app/api
COPY --from=builder /out/worker /app/worker
COPY migrations /app/migrations

ENV APP_PORT=8085
ENV WORKER_METRICS_PORT=8086
ENV MIGRATIONS_PATH=file:///app/migrations
EXPOSE 8085 8086

USER nonroot
ENTRYPOINT ["/app/api"]
