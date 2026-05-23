FROM cgr.dev/chainguard/go:latest AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /factvault ./cmd/factvault

FROM cgr.dev/chainguard/wolfi-base:latest AS runtime
RUN apk add --no-cache tini ca-certificates
WORKDIR /app
RUN mkdir -p /var/lib/factvault/auth && chown -R 65532:65532 /var/lib/factvault
COPY --from=builder /factvault /usr/local/bin/factvault
COPY --from=builder /app/migrations /app/migrations
COPY --from=builder /app/examples /app/examples
COPY --chmod=0555 deploy/docker/entrypoint.sh /usr/local/bin/factvault-entrypoint
USER 65532:65532
ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/factvault-entrypoint"]
CMD ["factvault"]
