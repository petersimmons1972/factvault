FROM cgr.dev/chainguard/wolfi-base:latest

# Install Postgres 16 and pgvector via wolfi apk
RUN apk add --no-cache \
    postgresql-16 \
    postgresql-16-contrib \
    pgvector-pg16 \
    tini

# pgvector is installed as a shared library; register it at container start
ENV POSTGRES_DB=factvault \
    POSTGRES_USER=factvault \
    PGDATA=/var/lib/postgresql/data

# Entrypoint: tini supervises postgres
USER 65532

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["postgres", "-D", "/var/lib/postgresql/data"]
