# syntax=docker/dockerfile:1
# ──────────────────────────────────────────────────────────────────────────────
# Factvault Embedder — BGE-M3 via sentence-transformers (CPU)
# Base: Chainguard wolfi-base (QC.7).  Runtime user: nonroot UID 65532.
# Model cache: bind /home/nonroot/.cache to a named volume so the model
# survives container restarts and is not re-downloaded on every boot.
#
# Single-stage build: the Chainguard free tier only exposes :latest and
# :latest-dev tags.  The python:latest-dev build stage uses Python 3.14 but
# wolfi-base installs Python 3.12, making the venv ABI-incompatible.  We
# resolve this by building the venv inside the runtime stage itself using
# wolfi's own python-3.12-dev package, which includes pip.
#
# Image size note: sentence-transformers pulls PyTorch (~1.5 GB) to serve
# the documented BAAI/bge-m3 model (1024-dim) exactly as specified in the
# README and schema.
# ──────────────────────────────────────────────────────────────────────────────
FROM cgr.dev/chainguard/wolfi-base:latest

# Install Python 3.12 (runtime + dev/pip), tini, TLS certs, and libgomp
# (required by PyTorch CPU kernels).
# python-3.12-dev brings pip; remove it from the final layer for a leaner image
# after the venv is built.
RUN apk add --no-cache \
      python-3.12 \
      python-3.12-dev \
      py3.12-pip \
      tini \
      ca-certificates-bundle \
      libgomp

WORKDIR /app

# Copy project manifest first for build-cache efficiency.
COPY pyproject.toml /app/pyproject.toml

# Build a self-contained venv under /opt/venv.
RUN python3.12 -m venv /opt/venv \
 && /opt/venv/bin/pip install --no-cache-dir \
      "fastapi>=0.111.0" \
      "uvicorn>=0.30.0" \
      "sentence-transformers>=3.3.1,<6"

# Remove pip/dev tools to trim the image; the venv is self-contained.
RUN if apk info python-3.12-dev >/dev/null 2>&1; then \
      apk del python-3.12-dev py3.12-pip; \
    fi

# Pre-create the cache directory owned by the nonroot UID so a freshly
# initialised Docker volume inherits the correct permissions.  Without this,
# `docker volume create` produces a root-owned mountpoint and the nonroot
# uvicorn process gets PermissionError on first HuggingFace download.
# (K8s users must still set fsGroup=65532 on the pod spec.)
RUN mkdir -p /home/nonroot/.cache \
 && chown -R 65532:65532 /home/nonroot

COPY app.py /app/app.py

ENV PATH="/opt/venv/bin:${PATH}" \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    # HuggingFace / sentence-transformers cache location.
    # Must match the compose volume mount (/home/nonroot/.cache).
    HF_HOME="/home/nonroot/.cache" \
    SENTENCE_TRANSFORMERS_HOME="/home/nonroot/.cache" \
    EMBEDDER_MODEL="BAAI/bge-m3"

# Chainguard nonroot UID — mandatory per QC.7.
# K8s pod specs must set fsGroup=65532 so the cache volume is writable.
USER 65532:65532

EXPOSE 8080

ENTRYPOINT ["/sbin/tini", "--"]
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8080"]
