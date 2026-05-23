FROM cgr.dev/chainguard/python:latest-dev AS build
WORKDIR /app
COPY pyproject.toml /app/pyproject.toml
COPY app.py /app/app.py
RUN python -m venv /opt/venv && /opt/venv/bin/pip install --no-cache-dir fastapi uvicorn

FROM cgr.dev/chainguard/wolfi-base:latest
RUN apk add --no-cache python3 tini
WORKDIR /app
COPY --from=build /opt/venv /opt/venv
COPY app.py /app/app.py
ENV PATH="/opt/venv/bin:${PATH}"
USER 65532:65532
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8080"]
