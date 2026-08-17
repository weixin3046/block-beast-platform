FROM golang:1.26.5 AS build
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -o /out/api ./cmd/api && \
    CGO_ENABLED=0 GOOS=linux go build -o /out/worker ./cmd/worker && \
    CGO_ENABLED=0 GOOS=linux go build -o /out/realtime ./cmd/realtime

FROM alpine:3.22
WORKDIR /app
COPY --from=build /out/ ./
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
USER 65532:65532
