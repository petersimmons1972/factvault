FROM python:3.12-slim AS runtime

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_DISABLE_PIP_VERSION_CHECK=1

WORKDIR /app
RUN pip install --no-cache-dir fastapi uvicorn \
    && useradd --uid 65532 --home-dir /home/nonroot --create-home --shell /usr/sbin/nologin nonroot

COPY services/embedder/app.py /app/app.py

USER 65532:65532
CMD ["uvicorn", "app:app", "--host", "0.0.0.0", "--port", "8080"]
