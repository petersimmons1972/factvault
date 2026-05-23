FROM cgr.dev/chainguard/go:latest AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /factvault ./cmd/factvault

FROM cgr.dev/chainguard/wolfi-base:latest AS runtime
RUN apk add --no-cache tini ca-certificates
COPY --from=builder /factvault /usr/local/bin/factvault
USER 65532:65532
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["factvault"]
