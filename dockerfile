# syntax=docker/dockerfile:1
# Build from repo root: docker build -f dockerfile -t arvaz-api:latest .

FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 go build -o /out/server ./cmd/server

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata docker-cli
WORKDIR /app
COPY --from=build /out/server /app/server
ENV HTTP_ADDR=0.0.0.0:8090
EXPOSE 8090
CMD ["/app/server"]
