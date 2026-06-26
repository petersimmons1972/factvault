# Pinned 2026-06-26: kept :latest-dev because pgvector-18 requires apk at build time.
# Switch to :latest (distroless) requires a multi-stage build to copy the .so — tracked separately.
# Re-pin with: docker buildx imagetools inspect cgr.dev/chainguard/postgres:latest-dev
FROM cgr.dev/chainguard/postgres:latest-dev@sha256:a6c6f4184836f7c2d5b445f84271a632607e4cb979158fbc0c6f1761cbbccf23

RUN apk add --no-cache pgvector-18

RUN rm -f /var/lib/postgres/initdb/docker-entrypoint.sh \
    /var/lib/postgres/initdb/docker-ensure-initdb.sh

ENV POSTGRES_DB=factvault \
    POSTGRES_USER=factvault \
    PGDATA=/var/lib/postgresql/data

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD pg_isready -U factvault || exit 1

USER 65532:65532

CMD ["-c", "listen_addresses=*"]
