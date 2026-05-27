FROM cgr.dev/chainguard/postgres:latest-dev

RUN apk add --no-cache pgvector-18

RUN rm -f /var/lib/postgres/initdb/docker-entrypoint.sh \
    /var/lib/postgres/initdb/docker-ensure-initdb.sh

ENV POSTGRES_DB=factvault \
    POSTGRES_USER=factvault \
    PGDATA=/var/lib/postgresql/data

CMD ["-c", "listen_addresses=*"]
