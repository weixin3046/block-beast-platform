FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/worker ./cmd/worker
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/realtime ./cmd/realtime

FROM gcr.io/distroless/static-debian12
WORKDIR /app
COPY --from=build /out/ ./
USER nonroot:nonroot