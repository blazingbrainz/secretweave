FROM golang:1.22-alpine AS builder

LABEL org.opencontainers.image.source=https://github.com/blazingbrainz/secretweave
LABEL org.opencontainers.image.description="SecretWeave: Kubernetes operator that syncs annotated Secrets across namespaces and reacts immediately to new namespace creation"
LABEL org.opencontainers.image.licenses="Apache-2.0"

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o /secretweave ./cmd/secretweave

FROM gcr.io/distroless/static:nonroot

COPY --from=builder /secretweave /secretweave
ENTRYPOINT ["/secretweave"]
