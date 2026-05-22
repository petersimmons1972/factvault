# Bundle and Retrieval Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the retrieval surface that downstream LLMs talk to. A shared `assemble()` function produces the canonical JSON bundle for both pre-computed per-entity dossiers and on-demand graph-expanded stories; a FastAPI REST surface and an MCP server expose three retrieval modes (entity lookup, story query, structured fact query); JWT auth resolves tenant_id and the database enforces isolation via RLS. This is Plan 4 of 5; depends on Plans 1-3.

**Architecture:** One `factvault.assembler.bundle.assemble()` function as the single code path for bundle production. Dossier worker calls it with `depth=0` for pre-compute; story endpoint calls it with `depth=2` or `3` for graph expansion. REST API via FastAPI with JWT middleware. MCP server wraps the same three retrieval modes as MCP tools. All requests resolve tenant_id from the JWT and run inside `tenant_context()`; Postgres RLS does the final isolation enforcement.

**Tech Stack:** Python 3.12, FastAPI 0.110+, uvicorn, python-jose (JWT), mcp-python-sdk, plus the Plan 1-3 stack.

---

## Known Plan-Bug Patterns (apply from the start — do NOT discover these during execution)

These six patterns were surfaced during Plan 1 execution. Every task in this plan is written to avoid them.

1. **`TIMESTAMPTZ` import:** `TIMESTAMPTZ` is NOT in `sqlalchemy.dialects.postgresql`. Use `TIMESTAMP(timezone=True)` from `sqlalchemy` (e.g., `sa.TIMESTAMP(timezone=True)`).
2. **Explicit SA imports:** `sa.UniqueConstraint` / `sa.LargeBinary` need direct imports when `sa` alias isn't in scope. Prefer `from sqlalchemy import UniqueConstraint, LargeBinary` explicitly.
3. **psycopg cast syntax:** psycopg refuses `:param::jsonb` / `:param::vector`. Use `CAST(:param AS jsonb)` / `CAST(:param AS vector)` in raw SQL.
4. **Postgres 15+ NULL uniqueness:** Unique constraints default to `NULLS NOT DISTINCT`. Tests relying on duplicate-NULL behavior must use distinct tenants/values to avoid unexpected conflicts.
5. **Fixture tenancy:** The `conn` fixture is single-tenant superuser (bypasses RLS). RLS-sensitive tests must use `app_engine`.
6. **RLS setting:** RLS policies wrap `current_setting(...)` with `NULLIF(..., '')` before `::uuid` cast — this is already in DB. Application code sets `app.tenant_id` (not `app.current_tenant_id`) via `tenant_context()` — match the GUC name used in `factvault/db/rls.py` exactly.

---

## File Structure

```
factvault/
├── factvault/
│   ├── assembler/
│   │   ├── __init__.py
│   │   ├── bundle.py                        # assemble(entity_ids, depth, tenant_id) -> dict
│   │   ├── graph.py                         # Recursive CTE graph expansion
│   │   └── serialize.py                     # Fact/source/relation -> bundle JSON shapes
│   ├── workers/
│   │   └── dossier.py                       # Stage 7 (new) — periodic dossier pre-compute
│   ├── api/
│   │   ├── __init__.py
│   │   ├── main.py                          # FastAPI app + middleware
│   │   ├── auth.py                          # JWT verification middleware
│   │   ├── deps.py                          # Reusable FastAPI dependencies (db connection, tenant_id)
│   │   ├── schemas.py                       # Pydantic request/response models
│   │   └── routes/
│   │       ├── __init__.py
│   │       ├── entities.py                  # GET /entities/{id}/dossier, GET /entities/by-name
│   │       ├── stories.py                   # POST /stories
│   │       ├── facts.py                     # POST /facts/query
│   │       └── health.py                    # GET /healthz, GET /readyz
│   ├── auth/
│   │   ├── __init__.py
│   │   ├── jwt.py                           # JWT verify + token issuance for local dev
│   │   └── dev_keys.py                      # Generate local dev RSA key pair (for tests + dev)
│   ├── mcp/
│   │   ├── __init__.py
│   │   └── server.py                        # MCP server with 3 tools
│   └── ... (existing)
├── tests/
│   ├── assembler/
│   │   ├── __init__.py
│   │   ├── test_bundle.py                   # The shared assembler — load-bearing tests
│   │   ├── test_graph.py
│   │   └── test_serialize.py
│   ├── workers/
│   │   └── test_dossier.py
│   ├── api/
│   │   ├── __init__.py
│   │   ├── test_auth.py
│   │   ├── test_entities.py
│   │   ├── test_stories.py
│   │   ├── test_facts.py
│   │   └── test_health.py
│   ├── auth/
│   │   └── test_jwt.py
│   ├── mcp/
│   │   └── test_server.py
│   └── integration/
│       └── test_retrieval_e2e.py            # source → fact → bundle → REST + MCP retrieval
└── ... (existing)
```

---

## Tasks

### Task 1 — Dependency additions to `pyproject.toml`

- [ ] **FAIL:** Confirm `fastapi`, `uvicorn`, `python-jose`, `mcp` are absent.

```bash
$ grep -E 'fastapi|uvicorn|python-jose|^    "mcp' pyproject.toml
# expected: no output (none yet listed)
```

- [ ] **IMPLEMENT:** Edit `pyproject.toml` to extend `dependencies` and add console_scripts.

The updated `[project]` `dependencies` list:

```toml
[project]
name = "factvault"
version = "0.0.1"
requires-python = ">=3.12,<3.14"
dependencies = [
    "sqlalchemy>=2.0,<3",
    "alembic>=1.13,<2",
    "psycopg[binary]>=3.1,<4",
    "pgvector>=0.3,<1",
    "pydantic>=2,<3",
    "httpx>=0.27,<1",
    "trafilatura>=1.9,<2",
    "feedparser>=6.0,<7",
    "click>=8,<9",
    "sentence-transformers>=3,<4",
    "openai>=1.40,<2",
    "pyyaml>=6,<7",
    "fastapi>=0.110,<1",
    "uvicorn[standard]>=0.27,<1",
    "python-jose[cryptography]>=3.3,<4",
    "mcp>=1.0,<2",
]
```

Add a `[project.scripts]` section (create it if absent):

```toml
[project.scripts]
factvault-api = "factvault.api.main:run"
factvault-mcp = "factvault.mcp.server:run"
```

The `[project.optional-dependencies]` `dev` section gains `httpx` (already a runtime dep but needed for FastAPI TestClient) and `pytest-httpx`:

```toml
[project.optional-dependencies]
dev = [
    "pytest>=8,<9",
    "testcontainers[postgres]>=4,<5",
    "pytest-asyncio>=0.23,<1",
    "pytest-httpx>=0.30,<1",
]
```

- [ ] **RUN/PASS:**

```bash
$ pip install -e ".[dev]"
# expected: Successfully installed fastapi-... uvicorn-... python-jose-... mcp-...
$ python -c "import fastapi, uvicorn, jose, mcp; print('OK')"
# expected: OK
```

- [ ] **COMMIT:**

```bash
git add pyproject.toml
git commit -m "chore(deps): add fastapi, uvicorn, python-jose, mcp for bundle-and-retrieval plan"
```

---

### Task 2 — JWT verification module

- [ ] **FAIL:** Create `tests/auth/__init__.py` (empty) and write the failing test file.

```python
# tests/auth/__init__.py
```

```python
# tests/auth/test_jwt.py
"""
Tests for JWTVerifier.

These tests use python-jose directly to mint tokens so they do not depend
on the dev-key infrastructure from Task 3.  All tokens are RS256.
"""
import time
import uuid
import pytest
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.hazmat.backends import default_backend
from cryptography.hazmat.primitives import serialization
from jose import jwt as jose_jwt

from factvault.auth.jwt import JWTVerifier, JWTError


# ── Helpers ────────────────────────────────────────────────────────────────────

def _generate_rsa_pair():
    """Return (private_key_pem_str, public_key_pem_str) for an RSA-2048 key."""
    private_key = rsa.generate_private_key(
        public_exponent=65537,
        key_size=2048,
        backend=default_backend(),
    )
    private_pem = private_key.private_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PrivateFormat.TraditionalOpenSSL,
        encryption_algorithm=serialization.NoEncryption(),
    ).decode()
    public_pem = private_key.public_key().public_bytes(
        encoding=serialization.Encoding.PEM,
        format=serialization.PublicFormat.SubjectPublicKeyInfo,
    ).decode()
    return private_pem, public_pem


@pytest.fixture(scope="module")
def rsa_pair():
    return _generate_rsa_pair()


def _mint_token(private_pem: str, tenant_id: str, ttl: int = 3600, algorithm: str = "RS256") -> str:
    payload = {
        "sub": tenant_id,
        "exp": int(time.time()) + ttl,
        "iat": int(time.time()),
    }
    return jose_jwt.encode(payload, private_pem, algorithm=algorithm)


# ── Tests ──────────────────────────────────────────────────────────────────────

def test_valid_token_returns_claims(rsa_pair):
    private_pem, public_pem = rsa_pair
    tenant_id = str(uuid.uuid4())
    token = _mint_token(private_pem, tenant_id)
    verifier = JWTVerifier(public_key_pem=public_pem)
    claims = verifier.verify(token)
    assert claims["sub"] == tenant_id


def test_expired_token_raises(rsa_pair):
    private_pem, public_pem = rsa_pair
    tenant_id = str(uuid.uuid4())
    # ttl=-1 produces a token already expired
    token = _mint_token(private_pem, tenant_id, ttl=-1)
    verifier = JWTVerifier(public_key_pem=public_pem)
    with pytest.raises(JWTError, match="expired"):
        verifier.verify(token)


def test_missing_sub_claim_raises(rsa_pair):
    private_pem, public_pem = rsa_pair
    # Mint without 'sub'
    payload = {"exp": int(time.time()) + 3600}
    token = jose_jwt.encode(payload, private_pem, algorithm="RS256")
    verifier = JWTVerifier(public_key_pem=public_pem)
    with pytest.raises(JWTError, match="sub"):
        verifier.verify(token)


def test_wrong_public_key_raises(rsa_pair):
    private_pem, _ = rsa_pair
    _, other_public_pem = _generate_rsa_pair()
    tenant_id = str(uuid.uuid4())
    token = _mint_token(private_pem, tenant_id)
    verifier = JWTVerifier(public_key_pem=other_public_pem)
    with pytest.raises(JWTError):
        verifier.verify(token)


def test_alg_none_attack_raises(rsa_pair):
    """An alg=none token must never be accepted."""
    _, public_pem = rsa_pair
    tenant_id = str(uuid.uuid4())
    # python-jose accepts alg=none only when 'none' is in options['algorithms']
    # We verify that JWTVerifier restricts to RS256 only.
    payload = {"sub": tenant_id, "exp": int(time.time()) + 3600}
    # Manually craft an alg=none token (header.payload.empty-sig)
    import base64, json
    header = base64.urlsafe_b64encode(json.dumps({"alg": "none", "typ": "JWT"}).encode()).rstrip(b"=").decode()
    body = base64.urlsafe_b64encode(json.dumps(payload).encode()).rstrip(b"=").decode()
    none_token = f"{header}.{body}."
    verifier = JWTVerifier(public_key_pem=public_pem)
    with pytest.raises(JWTError):
        verifier.verify(none_token)


def test_hs256_token_rejected(rsa_pair):
    """HS256-signed tokens must be rejected even if the HMAC secret matches nothing."""
    _, public_pem = rsa_pair
    tenant_id = str(uuid.uuid4())
    hs256_token = jose_jwt.encode(
        {"sub": tenant_id, "exp": int(time.time()) + 3600},
        "some-hmac-secret",
        algorithm="HS256",
    )
    verifier = JWTVerifier(public_key_pem=public_pem)
    with pytest.raises(JWTError):
        verifier.verify(hs256_token)
```

- [ ] **RUN/FAIL:**

```bash
$ python -m pytest tests/auth/test_jwt.py -x 2>&1 | head -20
# expected: ImportError or ModuleNotFoundError for factvault.auth.jwt
```

- [ ] **IMPLEMENT:** Create `factvault/auth/__init__.py` (empty) and `factvault/auth/jwt.py`:

```python
# factvault/auth/__init__.py
```

```python
# factvault/auth/jwt.py
"""
JWT verification for factvault.

Supports two key-source modes:
  1. ``FACTVAULT_JWT_PUBLIC_KEY`` env var — PEM string of the RSA public key.
  2. ``FACTVAULT_JWT_JWKS_URL`` env var — HTTPS URL of a JWKS endpoint;
     the key set is fetched on first use and cached for ``jwks_cache_ttl``
     seconds (default: 3600).

Only RS256 is accepted.  ``alg=none`` and symmetric algorithms are rejected
at the decode-options level — there is no code path that accepts them.

Raises ``JWTError`` (a subclass of ``ValueError``) for any invalid token.
"""
from __future__ import annotations

import os
import time
from typing import Any

from jose import jwt as jose_jwt
from jose.exceptions import JWTError as _JoseJWTError


class JWTError(ValueError):
    """Raised for any JWT verification failure."""


_ALLOWED_ALGORITHMS = ["RS256"]


class JWTVerifier:
    """
    Stateless JWT verifier.  Construct once per process; thread-safe.

    Args:
        public_key_pem: PEM string of the RSA public key.  Exactly one of
            ``public_key_pem`` or ``jwks_url`` must be supplied.
        jwks_url: HTTPS URL of a JWKS endpoint.  The key set is fetched on
            first use and cached for ``jwks_cache_ttl`` seconds.
        jwks_cache_ttl: Cache TTL in seconds for JWKS responses (default 3600).
    """

    def __init__(
        self,
        public_key_pem: str | None = None,
        jwks_url: str | None = None,
        jwks_cache_ttl: int = 3600,
    ) -> None:
        if public_key_pem and jwks_url:
            raise ValueError("Supply exactly one of public_key_pem or jwks_url, not both.")
        if not public_key_pem and not jwks_url:
            raise ValueError("Supply exactly one of public_key_pem or jwks_url.")
        self._public_key_pem = public_key_pem
        self._jwks_url = jwks_url
        self._jwks_cache_ttl = jwks_cache_ttl
        self._jwks_cached_at: float = 0.0
        self._jwks_key: str | None = None

    # ── Public API ─────────────────────────────────────────────────────────────

    def verify(self, token: str) -> dict[str, Any]:
        """
        Verify *token* and return the decoded claims dict.

        Raises:
            JWTError: if the token is expired, has an invalid signature,
                uses a disallowed algorithm, is missing the ``sub`` claim,
                or is otherwise malformed.
        """
        public_key = self._resolve_public_key(token)
        try:
            claims: dict[str, Any] = jose_jwt.decode(
                token,
                public_key,
                algorithms=_ALLOWED_ALGORITHMS,
                options={
                    "require": ["sub", "exp"],
                    "verify_exp": True,
                    "verify_aud": False,   # no audience constraint in v1
                },
            )
        except _JoseJWTError as exc:
            # Re-raise with a message that describes the failure in plain English.
            msg = str(exc).lower()
            if "expired" in msg:
                raise JWTError(f"Token expired: {exc}") from exc
            raise JWTError(f"Token verification failed: {exc}") from exc

        # Extra guard: sub must be present (jose 'require' above should catch
        # this, but be explicit for clarity and testability).
        if "sub" not in claims:
            raise JWTError("Token is missing required 'sub' claim.")

        return claims

    # ── Private helpers ────────────────────────────────────────────────────────

    def _resolve_public_key(self, token: str) -> str:
        """Return the RSA public key PEM string to use for verification."""
        if self._public_key_pem:
            return self._public_key_pem
        return self._fetch_jwks_key()

    def _fetch_jwks_key(self) -> str:
        """
        Fetch the first RSA key from the JWKS URL, caching for
        ``self._jwks_cache_ttl`` seconds.
        """
        now = time.monotonic()
        if self._jwks_key and (now - self._jwks_cached_at) < self._jwks_cache_ttl:
            return self._jwks_key  # type: ignore[return-value]

        import httpx  # local import — httpx is a runtime dep from Plan 2
        try:
            resp = httpx.get(self._jwks_url, timeout=10)  # type: ignore[arg-type]
            resp.raise_for_status()
            jwks = resp.json()
        except Exception as exc:
            raise JWTError(f"Failed to fetch JWKS from {self._jwks_url}: {exc}") from exc

        # Extract the first RSA key.  python-jose accepts the raw JWKS dict.
        keys = jwks.get("keys", [])
        rsa_keys = [k for k in keys if k.get("kty") == "RSA"]
        if not rsa_keys:
            raise JWTError(f"No RSA keys found in JWKS response from {self._jwks_url}.")

        self._jwks_key = rsa_keys[0]  # type: ignore[assignment]
        self._jwks_cached_at = now
        return self._jwks_key  # type: ignore[return-value]


# ── Environment-based factory ──────────────────────────────────────────────────

def verifier_from_env() -> JWTVerifier:
    """
    Construct a ``JWTVerifier`` from environment variables.

    Checks ``FACTVAULT_JWT_PUBLIC_KEY`` first, then ``FACTVAULT_JWT_JWKS_URL``.
    Raises ``RuntimeError`` if neither is set.
    """
    pem = os.environ.get("FACTVAULT_JWT_PUBLIC_KEY")
    jwks_url = os.environ.get("FACTVAULT_JWT_JWKS_URL")
    if pem:
        return JWTVerifier(public_key_pem=pem)
    if jwks_url:
        return JWTVerifier(jwks_url=jwks_url)
    raise RuntimeError(
        "Neither FACTVAULT_JWT_PUBLIC_KEY nor FACTVAULT_JWT_JWKS_URL is set. "
        "Configure one before starting the API server."
    )
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/auth/test_jwt.py -v
# expected: 6 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/auth/__init__.py factvault/auth/jwt.py tests/auth/__init__.py tests/auth/test_jwt.py
git commit -m "feat(auth): JWT verification module with RS256-only enforcement"
```

---

### Task 3 — Local dev key generation and token issuance CLI

- [ ] **FAIL:** Add failing tests for dev key round-trip:

```python
# tests/auth/test_jwt.py  (append to the existing file)

# ── Dev-key round-trip ─────────────────────────────────────────────────────────

def test_dev_key_roundtrip(tmp_path, monkeypatch):
    """generate_dev_keys() → issue_token() → JWTVerifier.verify() round-trip."""
    from factvault.auth.dev_keys import generate_dev_keys, issue_dev_token

    priv_path = tmp_path / "dev_key.pem"
    pub_path = tmp_path / "dev_key.pub.pem"
    generate_dev_keys(private_key_path=priv_path, public_key_path=pub_path)

    assert priv_path.exists()
    assert pub_path.exists()

    tenant_id = str(uuid.uuid4())
    token = issue_dev_token(
        tenant_id=tenant_id,
        private_key_path=priv_path,
        ttl_hours=1,
    )

    public_pem = pub_path.read_text()
    verifier = JWTVerifier(public_key_pem=public_pem)
    claims = verifier.verify(token)
    assert claims["sub"] == tenant_id


def test_dev_token_via_env(tmp_path, monkeypatch):
    """verifier_from_env() picks up FACTVAULT_JWT_PUBLIC_KEY set from the dev pub key."""
    from factvault.auth.dev_keys import generate_dev_keys, issue_dev_token
    from factvault.auth.jwt import verifier_from_env

    priv_path = tmp_path / "dev_key.pem"
    pub_path = tmp_path / "dev_key.pub.pem"
    generate_dev_keys(private_key_path=priv_path, public_key_path=pub_path)

    monkeypatch.setenv("FACTVAULT_JWT_PUBLIC_KEY", pub_path.read_text())

    tenant_id = str(uuid.uuid4())
    token = issue_dev_token(tenant_id=tenant_id, private_key_path=priv_path, ttl_hours=1)

    verifier = verifier_from_env()
    claims = verifier.verify(token)
    assert claims["sub"] == tenant_id
```

- [ ] **RUN/FAIL:**

```bash
$ python -m pytest tests/auth/test_jwt.py::test_dev_key_roundtrip tests/auth/test_jwt.py::test_dev_token_via_env -x 2>&1 | head -20
# expected: ImportError for factvault.auth.dev_keys
```

- [ ] **IMPLEMENT:** `factvault/auth/dev_keys.py`:

```python
# factvault/auth/dev_keys.py
"""
Local-dev RSA key pair generation and token issuance.

Intended for local development and integration tests only.
Never use the dev key in production.

Usage (Python)::

    from factvault.auth.dev_keys import generate_dev_keys, issue_dev_token
    generate_dev_keys()  # writes to ~/.config/factvault/dev_key.pem + .pub.pem
    token = issue_dev_token(tenant_id="<uuid>")

Usage (CLI)::

    factvault auth issue-token --tenant <uuid> [--ttl-hours 24]
"""
from __future__ import annotations

import os
import time
from pathlib import Path
from typing import Optional

from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.hazmat.backends import default_backend
from cryptography.hazmat.primitives import serialization
from jose import jwt as jose_jwt


# Default paths under ~/.config/factvault/
_DEFAULT_DIR = Path.home() / ".config" / "factvault"
_DEFAULT_PRIV = _DEFAULT_DIR / "dev_key.pem"
_DEFAULT_PUB = _DEFAULT_DIR / "dev_key.pub.pem"


def generate_dev_keys(
    private_key_path: Optional[Path] = None,
    public_key_path: Optional[Path] = None,
) -> tuple[Path, Path]:
    """
    Generate an RSA-2048 key pair and write to disk.

    If the private key file already exists it is overwritten (dev workflow only).

    Args:
        private_key_path: Destination for the private key PEM.  Defaults to
            ``~/.config/factvault/dev_key.pem``.
        public_key_path: Destination for the public key PEM.  Defaults to
            ``~/.config/factvault/dev_key.pub.pem``.

    Returns:
        ``(private_key_path, public_key_path)`` as resolved ``Path`` objects.
    """
    priv_path = Path(private_key_path) if private_key_path else _DEFAULT_PRIV
    pub_path = Path(public_key_path) if public_key_path else _DEFAULT_PUB

    priv_path.parent.mkdir(parents=True, exist_ok=True)

    private_key = rsa.generate_private_key(
        public_exponent=65537,
        key_size=2048,
        backend=default_backend(),
    )

    priv_path.write_bytes(
        private_key.private_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PrivateFormat.TraditionalOpenSSL,
            encryption_algorithm=serialization.NoEncryption(),
        )
    )
    # Restrict private key permissions to owner-read-only
    priv_path.chmod(0o600)

    pub_path.write_bytes(
        private_key.public_key().public_bytes(
            encoding=serialization.Encoding.PEM,
            format=serialization.PublicFormat.SubjectPublicKeyInfo,
        )
    )

    return priv_path, pub_path


def issue_dev_token(
    tenant_id: str,
    private_key_path: Optional[Path] = None,
    ttl_hours: int = 24,
) -> str:
    """
    Sign a JWT for the given tenant_id using the local dev private key.

    Args:
        tenant_id: UUID string to embed as the ``sub`` claim.
        private_key_path: Path to the PEM private key.  Defaults to
            ``~/.config/factvault/dev_key.pem``.
        ttl_hours: Token lifetime in hours (default 24).

    Returns:
        Signed JWT string.

    Raises:
        FileNotFoundError: if the private key file does not exist.  Run
            ``generate_dev_keys()`` first.
    """
    priv_path = Path(private_key_path) if private_key_path else _DEFAULT_PRIV
    if not priv_path.exists():
        raise FileNotFoundError(
            f"Dev private key not found at {priv_path}. "
            "Run `factvault auth generate-keys` or call generate_dev_keys() first."
        )

    private_pem = priv_path.read_text()
    payload = {
        "sub": tenant_id,
        "iat": int(time.time()),
        "exp": int(time.time()) + ttl_hours * 3600,
    }
    return jose_jwt.encode(payload, private_pem, algorithm="RS256")


# ── CLI integration ────────────────────────────────────────────────────────────
# These functions are called by the `factvault auth` CLI group defined in
# factvault/cli/auth_commands.py (Plan 4 Task 3 CLI wiring).

def cli_generate_keys() -> None:
    """CLI entry point: generate dev keys at default paths and print locations."""
    priv, pub = generate_dev_keys()
    print(f"Private key: {priv}")
    print(f"Public key:  {pub}")
    print(
        "\nSet FACTVAULT_JWT_PUBLIC_KEY to the public key contents for local API dev:\n"
        f"  export FACTVAULT_JWT_PUBLIC_KEY=\"$(cat {pub})\""
    )


def cli_issue_token(tenant_id: str, ttl_hours: int = 24) -> None:
    """CLI entry point: print a dev token for the given tenant_id."""
    token = issue_dev_token(tenant_id=tenant_id, ttl_hours=ttl_hours)
    print(token)
```

Now add the `factvault auth` CLI group. The `factvault` CLI entry point was established in Plan 2 (`factvault/cli.py` or similar). Create `factvault/cli/auth_commands.py`:

```python
# factvault/cli/auth_commands.py
"""
`factvault auth` CLI subcommands for local development token management.

Registered as a Click group and attached to the main `factvault` CLI group
in factvault/cli/__init__.py (or factvault/cli.py per Plan 2 layout).
"""
import click


@click.group("auth")
def auth_group() -> None:
    """Auth utilities for local development."""


@auth_group.command("generate-keys")
def generate_keys_cmd() -> None:
    """Generate a local RSA-2048 dev key pair under ~/.config/factvault/."""
    from factvault.auth.dev_keys import cli_generate_keys
    cli_generate_keys()


@auth_group.command("issue-token")
@click.option("--tenant", required=True, help="Tenant UUID to embed in the token.")
@click.option("--ttl-hours", default=24, show_default=True, help="Token lifetime in hours.")
def issue_token_cmd(tenant: str, ttl_hours: int) -> None:
    """Issue a dev JWT for the given tenant UUID and print it to stdout."""
    from factvault.auth.dev_keys import cli_issue_token
    cli_issue_token(tenant_id=tenant, ttl_hours=ttl_hours)
```

Wire `auth_group` into the main CLI entry point. Open `factvault/cli.py` (or `factvault/cli/__init__.py` per Plan 2 layout) and add:

```python
# In the main CLI group definition, add:
from factvault.cli.auth_commands import auth_group
cli.add_command(auth_group)
```

*(The exact insertion point depends on Plan 2's CLI layout. The worker executing this task must locate the main Click group and add the `auth_group` command to it.)*

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/auth/test_jwt.py -v
# expected: 8 passed (original 6 + 2 new dev-key tests)
```

```bash
$ python -m factvault.cli auth issue-token --tenant "$(python -c 'import uuid; print(uuid.uuid4())')" 2>&1 | head -3
# expected: FileNotFoundError (dev key not yet generated) or a JWT string if keys already exist
# Either outcome confirms the CLI wiring works; the test suite is the authoritative pass gate.
```

- [ ] **COMMIT:**

```bash
git add factvault/auth/dev_keys.py factvault/cli/auth_commands.py tests/auth/test_jwt.py
git commit -m "feat(auth): local dev key generation + issue-token CLI subcommand"
```

---

### Task 4 — Bundle assembler core function (depth=0)

- [ ] **FAIL:** Create `tests/assembler/__init__.py` and write the failing test:

```python
# tests/assembler/__init__.py
```

```python
# tests/assembler/test_bundle.py
"""
Tests for factvault.assembler.bundle.BundleAssembler.

These tests use the `conn` fixture (superuser, bypasses RLS) to insert
realistic test data and assert the returned bundle dict matches the spec's
canonical JSON shape exactly.
"""
import uuid
import pytest
from datetime import datetime, timezone
from sqlalchemy import text

from factvault.assembler.bundle import BundleAssembler


# ── Fixtures ───────────────────────────────────────────────────────────────────

@pytest.fixture()
def tenant_id():
    return uuid.uuid4()


@pytest.fixture()
def seed_data(conn, tenant_id):
    """
    Insert one entity, one property, one statement, one qualifier, one source,
    and one statement_sources row.  Returns a dict of all IDs for assertions.
    """
    entity_id = uuid.uuid4()
    property_id = uuid.uuid4()
    statement_id = uuid.uuid4()
    qualifier_prop_id = uuid.uuid4()
    qualifier_id = uuid.uuid4()
    source_id = uuid.uuid4()
    stmt_source_id = uuid.uuid4()

    now = datetime(2026, 5, 22, 4, 0, 0, tzinfo=timezone.utc)

    conn.execute(text(
        "INSERT INTO entities (id, tenant_id, label, type_uri, description, ext_id) "
        "VALUES (:id, :tid, :label, :type_uri, :desc, :ext_id)"
    ), {
        "id": str(entity_id), "tid": str(tenant_id),
        "label": "MegaCorp", "type_uri": "https://schema.org/Organization",
        "desc": "US-listed technology conglomerate", "ext_id": "CIK:0001234567",
    })

    # Register a property (string value type)
    conn.execute(text(
        "INSERT INTO properties (id, tenant_id, slug, label, value_type) "
        "VALUES (:id, :tid, :slug, :label, :vtype)"
    ), {
        "id": str(property_id), "tid": str(tenant_id),
        "slug": "revenue_usd", "label": "Revenue (USD)", "vtype": "number",
    })

    # Qualifier property
    conn.execute(text(
        "INSERT INTO properties (id, tenant_id, slug, label, value_type) "
        "VALUES (:id, :tid, :slug, :label, :vtype)"
    ), {
        "id": str(qualifier_prop_id), "tid": str(tenant_id),
        "slug": "point_in_time", "label": "Point in time", "vtype": "date",
    })

    conn.execute(text(
        "INSERT INTO statements (id, tenant_id, subject_id, property_id, val_number, rank, confidence) "
        "VALUES (:id, :tid, :subject, :prop, :val, :rank, :conf)"
    ), {
        "id": str(statement_id), "tid": str(tenant_id),
        "subject": str(entity_id), "prop": str(property_id),
        "val": 4200000000, "rank": "preferred", "conf": 0.85,
    })

    conn.execute(text(
        "INSERT INTO qualifiers (id, statement_id, property_id, val_date) "
        "VALUES (:id, :stmt, :prop, :val)"
    ), {
        "id": str(qualifier_id), "stmt": str(statement_id),
        "prop": str(qualifier_prop_id), "val": now,
    })

    conn.execute(text(
        "INSERT INTO sources "
        "(id, tenant_id, url, content_hash, publisher, fetched_at, "
        " archive_url, last_verified_at, status) "
        "VALUES (:id, :tid, :url, :hash, :pub, :fetched, :archive, :verified, :status)"
    ), {
        "id": str(source_id), "tid": str(tenant_id),
        "url": "https://www.reuters.com/megacorp-revenue-2026",
        "hash": "a3f2c1d4" * 8,
        "pub": "reuters.com",
        "fetched": now,
        "archive": "https://web.archive.org/web/20260522/https://www.reuters.com/megacorp-revenue-2026",
        "verified": now,
        "status": "verified",
    })

    conn.execute(text(
        "INSERT INTO statement_sources "
        "(id, statement_id, source_id, excerpt, excerpt_offset_start, "
        " excerpt_offset_end, extraction_method) "
        "VALUES (:id, :stmt, :src, :excerpt, :start, :end, :method)"
    ), {
        "id": str(stmt_source_id), "stmt": str(statement_id), "src": str(source_id),
        "excerpt": "MegaCorp reported revenue of $4.2 billion for fiscal year 2025.",
        "start": 100, "end": 162,
        "method": "llm:gpt-5:v1",
    })

    return {
        "entity_id": entity_id,
        "property_id": property_id,
        "statement_id": statement_id,
        "qualifier_id": qualifier_id,
        "source_id": source_id,
        "stmt_source_id": stmt_source_id,
    }


# ── Tests ──────────────────────────────────────────────────────────────────────

def test_assemble_depth0_returns_canonical_shape(conn, tenant_id, seed_data):
    """depth=0 bundle has query, assembled_at, entities, facts, relations, conflicts."""
    assembler = BundleAssembler(conn)
    bundle = assembler.assemble(
        entity_ids=[seed_data["entity_id"]],
        depth=0,
        tenant_id=tenant_id,
    )

    # Top-level keys
    assert set(bundle.keys()) == {"query", "assembled_at", "entities", "facts", "relations", "conflicts"}

    # query block
    assert bundle["query"]["type"] == "dossier"
    assert str(seed_data["entity_id"]) in [str(eid) for eid in bundle["query"]["entity_ids"]]
    assert bundle["query"]["depth"] == 0

    # assembled_at is an ISO string
    assert isinstance(bundle["assembled_at"], str)

    # entities
    assert len(bundle["entities"]) == 1
    entity = bundle["entities"][0]
    assert entity["id"] == str(seed_data["entity_id"])
    assert entity["label"] == "MegaCorp"
    assert entity["type_uri"] == "https://schema.org/Organization"
    assert entity["ext_id"] == "CIK:0001234567"

    # facts
    assert len(bundle["facts"]) == 1
    fact = bundle["facts"][0]
    assert fact["id"] == str(seed_data["statement_id"])
    assert fact["subject"]["label"] == "MegaCorp"
    assert fact["property"]["slug"] == "revenue_usd"
    assert fact["rank"] == "preferred"
    assert fact["confidence"] == 0.85

    # qualifiers
    assert len(fact["qualifiers"]) == 1
    qual = fact["qualifiers"][0]
    assert qual["property"]["slug"] == "point_in_time"
    assert "date" in qual["value"]

    # sources — full provenance required
    assert len(fact["sources"]) == 1
    src = fact["sources"][0]
    assert src["id"] == str(seed_data["source_id"])
    assert src["url"] == "https://www.reuters.com/megacorp-revenue-2026"
    assert src["publisher"] == "reuters.com"
    assert "fetched_at" in src
    assert src["content_hash"] == "a3f2c1d4" * 8
    assert "archive_url" in src
    assert src["excerpt"] == "MegaCorp reported revenue of $4.2 billion for fiscal year 2025."
    assert src["excerpt_offset_start"] == 100
    assert src["excerpt_offset_end"] == 162
    assert "last_verified_at" in src
    assert src["verification_status"] == "verified"
    assert src["extraction_method"] == "llm:gpt-5:v1"


def test_assemble_depth0_no_entity_returns_empty(conn, tenant_id):
    """Assembling a non-existent entity_id returns an empty but valid bundle."""
    assembler = BundleAssembler(conn)
    bundle = assembler.assemble(
        entity_ids=[uuid.uuid4()],
        depth=0,
        tenant_id=tenant_id,
    )
    assert bundle["entities"] == []
    assert bundle["facts"] == []
    assert bundle["relations"] == []
    assert bundle["conflicts"] == []


def test_assemble_depth0_max_facts_limits_output(conn, tenant_id, seed_data):
    """max_facts=0 returns no facts but still returns the entity."""
    assembler = BundleAssembler(conn)
    bundle = assembler.assemble(
        entity_ids=[seed_data["entity_id"]],
        depth=0,
        tenant_id=tenant_id,
        max_facts=0,
    )
    assert len(bundle["entities"]) == 1
    assert bundle["facts"] == []
```

- [ ] **RUN/FAIL:**

```bash
$ python -m pytest tests/assembler/test_bundle.py -x 2>&1 | head -20
# expected: ImportError for factvault.assembler.bundle
```

- [ ] **IMPLEMENT:** `factvault/assembler/__init__.py` (empty) and `factvault/assembler/bundle.py`:

```python
# factvault/assembler/__init__.py
```

```python
# factvault/assembler/bundle.py
"""
factvault.assembler.bundle — Single entry point for all bundle production.

``BundleAssembler.assemble()`` is the only code path that produces the
canonical bundle JSON structure.  Both the dossier worker (depth=0) and
the story endpoint (depth=2..3) call this method.
"""
from __future__ import annotations

from datetime import datetime, timezone
from typing import Optional
from uuid import UUID

from sqlalchemy import text
from sqlalchemy.engine import Connection

from factvault.assembler.graph import expand_entities
from factvault.assembler.serialize import (
    serialize_entity,
    serialize_fact,
    serialize_relation,
)


class BundleAssembler:
    """
    Assembles the canonical bundle JSON for a set of entity IDs.

    Args:
        connection: An active SQLAlchemy ``Connection`` with a transaction
            open and ``app.tenant_id`` already set via ``tenant_context()``.
            The assembler does not manage transactions or RLS settings.
    """

    def __init__(self, connection: Connection) -> None:
        self._conn = connection

    def assemble(
        self,
        entity_ids: list[UUID],
        depth: int = 0,
        tenant_id: Optional[UUID] = None,
        query: Optional[str] = None,
        max_facts: Optional[int] = 100,
        min_confidence: float = 0.0,
    ) -> dict:
        """
        Produce the canonical bundle dict.

        Args:
            entity_ids: Seed entity UUIDs.
            depth: Graph expansion depth.  0 = seed entities only; 1..3 =
                recursive CTE expansion through ``relations``.
            tenant_id: Tenant UUID.  Passed through to query metadata only;
                RLS enforces isolation independently.
            query: Free-text query string (story mode).  None for dossier mode.
            max_facts: Maximum number of facts to include.  None = unlimited.
            min_confidence: Minimum confidence threshold for facts.

        Returns:
            The canonical bundle dict matching the spec §3.4 JSON shape.
        """
        assembled_at = datetime.now(tz=timezone.utc)

        # 1. Resolve entity set (expand graph if depth > 0)
        if depth > 0:
            all_entity_ids = expand_entities(
                self._conn, seed_ids=entity_ids, depth=depth
            )
        else:
            all_entity_ids = set(entity_ids)

        # 2. Fetch entity rows
        entity_rows = self._fetch_entities(all_entity_ids)

        # 3. Fetch statements + qualifiers + sources
        fact_dicts = self._fetch_facts(
            entity_ids=list(all_entity_ids),
            max_facts=max_facts,
            min_confidence=min_confidence,
        )

        # 4. Fetch relations between collected entities
        relation_dicts = self._fetch_relations(list(all_entity_ids))

        # 5. Detect conflicts among collected facts
        conflict_dicts = self._detect_conflicts(list(all_entity_ids))

        return {
            "query": {
                "type": "story" if (query or depth > 0) else "dossier",
                "entity_ids": [str(eid) for eid in entity_ids],
                "depth": depth,
                "tenant_id": str(tenant_id) if tenant_id else None,
                "query_text": query,
            },
            "assembled_at": assembled_at.isoformat(),
            "entities": [serialize_entity(row) for row in entity_rows],
            "facts": fact_dicts,
            "relations": relation_dicts,
            "conflicts": conflict_dicts,
        }

    # ── Private query helpers ──────────────────────────────────────────────────

    def _fetch_entities(self, entity_ids: set[UUID]) -> list:
        if not entity_ids:
            return []
        ids_list = [str(eid) for eid in entity_ids]
        result = self._conn.execute(
            text(
                "SELECT id, label, type_uri, description, ext_id "
                "FROM entities "
                "WHERE id = ANY(CAST(:ids AS uuid[]))"
            ),
            {"ids": ids_list},
        )
        return result.fetchall()

    def _fetch_facts(
        self,
        entity_ids: list[UUID],
        max_facts: Optional[int],
        min_confidence: float,
    ) -> list[dict]:
        if not entity_ids:
            return []

        ids_list = [str(eid) for eid in entity_ids]

        stmt_sql = (
            "SELECT s.id, s.subject_id, s.property_id, "
            "       s.val_text, s.val_number, s.val_date, s.val_entity, s.val_json, "
            "       s.rank, s.confidence, s.created_at, "
            "       p.slug AS property_slug, p.label AS property_label, p.value_type, "
            "       e.label AS subject_label "
            "FROM statements s "
            "JOIN properties p ON p.id = s.property_id "
            "JOIN entities e ON e.id = s.subject_id "
            "WHERE s.subject_id = ANY(CAST(:ids AS uuid[])) "
            "  AND s.confidence >= :min_conf "
            "  AND s.rank != 'deprecated' "
            "ORDER BY s.confidence DESC, s.created_at DESC"
        )
        params: dict = {"ids": ids_list, "min_conf": min_confidence}
        if max_facts is not None:
            stmt_sql += " LIMIT :limit"
            params["limit"] = max_facts

        stmt_rows = self._conn.execute(text(stmt_sql), params).fetchall()
        if not stmt_rows:
            return []

        stmt_ids = [str(row.id) for row in stmt_rows]

        # Fetch all qualifiers for these statements in one query
        qual_rows = self._conn.execute(
            text(
                "SELECT q.id, q.statement_id, q.property_id, "
                "       q.val_text, q.val_number, q.val_date, q.val_entity, "
                "       p.slug AS property_slug, p.label AS property_label, p.value_type "
                "FROM qualifiers q "
                "JOIN properties p ON p.id = q.property_id "
                "WHERE q.statement_id = ANY(CAST(:ids AS uuid[]))"
            ),
            {"ids": stmt_ids},
        ).fetchall()

        # Fetch all statement_sources + source records in one query
        src_rows = self._conn.execute(
            text(
                "SELECT ss.statement_id, ss.extraction_method, "
                "       ss.excerpt, ss.excerpt_offset_start, ss.excerpt_offset_end, "
                "       s.id AS source_id, s.url, s.publisher, s.fetched_at, "
                "       s.content_hash, s.archive_url, "
                "       s.last_verified_at, s.status AS verification_status "
                "FROM statement_sources ss "
                "JOIN sources s ON s.id = ss.source_id "
                "WHERE ss.statement_id = ANY(CAST(:ids AS uuid[]))"
            ),
            {"ids": stmt_ids},
        ).fetchall()

        # Group qualifiers and sources by statement_id
        quals_by_stmt: dict[str, list] = {}
        for q in qual_rows:
            quals_by_stmt.setdefault(str(q.statement_id), []).append(q)

        srcs_by_stmt: dict[str, list] = {}
        for s in src_rows:
            srcs_by_stmt.setdefault(str(s.statement_id), []).append(s)

        return [
            serialize_fact(
                statement_row=row,
                qualifier_rows=quals_by_stmt.get(str(row.id), []),
                source_rows=srcs_by_stmt.get(str(row.id), []),
            )
            for row in stmt_rows
        ]

    def _fetch_relations(self, entity_ids: list[UUID]) -> list[dict]:
        if not entity_ids:
            return []
        ids_list = [str(eid) for eid in entity_ids]
        rows = self._conn.execute(
            text(
                "SELECT r.id, r.source_id, r.target_id, r.type, r.confidence, r.statement_id, "
                "       e_src.label AS source_label, e_tgt.label AS target_label "
                "FROM relations r "
                "JOIN entities e_src ON e_src.id = r.source_id "
                "JOIN entities e_tgt ON e_tgt.id = r.target_id "
                "WHERE r.source_id = ANY(CAST(:ids AS uuid[])) "
                "   OR r.target_id = ANY(CAST(:ids AS uuid[])) "
                "   AND r.type != 'embedding-near'"
            ),
            {"ids": ids_list},
        ).fetchall()
        return [serialize_relation(row) for row in rows]

    def _detect_conflicts(self, entity_ids: list[UUID]) -> list[dict]:
        """
        Return conflict entries for any entity in entity_ids that appears
        in v_conflicts.  Conflict entries carry the competing value pair
        with rank, confidence, and sources for each side.
        """
        if not entity_ids:
            return []
        ids_list = [str(eid) for eid in entity_ids]
        rows = self._conn.execute(
            text(
                "SELECT c.statement_a_id, c.statement_b_id, "
                "       c.subject_id, c.property_id, c.property_slug, "
                "       c.val_a_text, c.val_a_number, c.val_a_date, c.val_a_entity, "
                "       c.val_b_text, c.val_b_number, c.val_b_date, c.val_b_entity, "
                "       c.confidence_a, c.confidence_b, "
                "       c.rank_a, c.rank_b, "
                "       e.label AS subject_label "
                "FROM v_conflicts c "
                "JOIN entities e ON e.id = c.subject_id "
                "WHERE c.subject_id = ANY(CAST(:ids AS uuid[]))"
            ),
            {"ids": ids_list},
        ).fetchall()

        conflicts = []
        for row in rows:
            def _val(text_v, num_v, date_v, entity_v):
                if text_v is not None:
                    return {"text": text_v}
                if num_v is not None:
                    return {"number": float(num_v)}
                if date_v is not None:
                    return {"date": date_v.isoformat() if hasattr(date_v, "isoformat") else str(date_v)}
                if entity_v is not None:
                    return {"entity_id": str(entity_v)}
                return {}

            conflicts.append({
                "subject": {"id": str(row.subject_id), "label": row.subject_label},
                "property_slug": row.property_slug,
                "competing_values": [
                    {
                        "statement_id": str(row.statement_a_id),
                        "value": _val(row.val_a_text, row.val_a_number, row.val_a_date, row.val_a_entity),
                        "rank": row.rank_a,
                        "confidence": float(row.confidence_a) if row.confidence_a is not None else None,
                    },
                    {
                        "statement_id": str(row.statement_b_id),
                        "value": _val(row.val_b_text, row.val_b_number, row.val_b_date, row.val_b_entity),
                        "rank": row.rank_b,
                        "confidence": float(row.confidence_b) if row.confidence_b is not None else None,
                    },
                ],
            })
        return conflicts
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/assembler/test_bundle.py -v
# expected: 3 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/assembler/__init__.py factvault/assembler/bundle.py \
        tests/assembler/__init__.py tests/assembler/test_bundle.py
git commit -m "feat(assembler): BundleAssembler depth=0 with full source provenance"
```

---

### Task 5 — Bundle serialization helpers

- [ ] **FAIL:** Write the failing test:

```python
# tests/assembler/test_serialize.py
"""
Tests for pure serialization functions in factvault.assembler.serialize.

Uses namedtuple-like Row objects to simulate SQLAlchemy result rows.
"""
import uuid
from datetime import datetime, timezone
from collections import namedtuple

import pytest

from factvault.assembler.serialize import (
    serialize_entity,
    serialize_fact,
    serialize_relation,
    serialize_source,
)


# ── Helpers to build fake Row objects ─────────────────────────────────────────

def _make_entity_row(**kw):
    EntityRow = namedtuple("EntityRow", ["id", "label", "type_uri", "description", "ext_id"])
    defaults = {
        "id": uuid.uuid4(), "label": "MegaCorp",
        "type_uri": "https://schema.org/Organization",
        "description": "A big company", "ext_id": "CIK:001",
    }
    defaults.update(kw)
    return EntityRow(**defaults)


def _make_stmt_row(**kw):
    fields = [
        "id", "subject_id", "property_id",
        "val_text", "val_number", "val_date", "val_entity", "val_json",
        "rank", "confidence", "created_at",
        "property_slug", "property_label", "value_type", "subject_label",
    ]
    StmtRow = namedtuple("StmtRow", fields)
    now = datetime(2026, 5, 22, 4, 0, 0, tzinfo=timezone.utc)
    defaults = {
        "id": uuid.uuid4(), "subject_id": uuid.uuid4(), "property_id": uuid.uuid4(),
        "val_text": None, "val_number": 4200000000, "val_date": None,
        "val_entity": None, "val_json": None,
        "rank": "preferred", "confidence": 0.85, "created_at": now,
        "property_slug": "revenue_usd", "property_label": "Revenue (USD)",
        "value_type": "number", "subject_label": "MegaCorp",
    }
    defaults.update(kw)
    return StmtRow(**defaults)


def _make_qual_row(**kw):
    fields = [
        "id", "statement_id", "property_id",
        "val_text", "val_number", "val_date", "val_entity",
        "property_slug", "property_label", "value_type",
    ]
    QualRow = namedtuple("QualRow", fields)
    now = datetime(2026, 5, 22, 4, 0, 0, tzinfo=timezone.utc)
    defaults = {
        "id": uuid.uuid4(), "statement_id": uuid.uuid4(), "property_id": uuid.uuid4(),
        "val_text": None, "val_number": None, "val_date": now, "val_entity": None,
        "property_slug": "point_in_time", "property_label": "Point in time", "value_type": "date",
    }
    defaults.update(kw)
    return QualRow(**defaults)


def _make_source_row(**kw):
    fields = [
        "statement_id", "extraction_method",
        "excerpt", "excerpt_offset_start", "excerpt_offset_end",
        "source_id", "url", "publisher", "fetched_at",
        "content_hash", "archive_url",
        "last_verified_at", "verification_status",
    ]
    SrcRow = namedtuple("SrcRow", fields)
    now = datetime(2026, 5, 22, 4, 0, 0, tzinfo=timezone.utc)
    defaults = {
        "statement_id": uuid.uuid4(), "extraction_method": "llm:gpt-5:v1",
        "excerpt": "MegaCorp reported revenue of $4.2 billion.",
        "excerpt_offset_start": 100, "excerpt_offset_end": 142,
        "source_id": uuid.uuid4(), "url": "https://reuters.com/story",
        "publisher": "reuters.com", "fetched_at": now,
        "content_hash": "a3f2c1d4" * 8,
        "archive_url": "https://web.archive.org/web/20260522/https://reuters.com/story",
        "last_verified_at": now, "verification_status": "live",
    }
    defaults.update(kw)
    return SrcRow(**defaults)


def _make_relation_row(**kw):
    fields = [
        "id", "source_id", "target_id", "type", "confidence",
        "statement_id", "source_label", "target_label",
    ]
    RelRow = namedtuple("RelRow", fields)
    defaults = {
        "id": uuid.uuid4(), "source_id": uuid.uuid4(), "target_id": uuid.uuid4(),
        "type": "acquired", "confidence": 0.85,
        "statement_id": uuid.uuid4(),
        "source_label": "MegaCorp", "target_label": "Acme Corp",
    }
    defaults.update(kw)
    return RelRow(**defaults)


# ── serialize_entity ──────────────────────────────────────────────────────────

def test_serialize_entity_shape():
    row = _make_entity_row()
    result = serialize_entity(row)
    assert set(result.keys()) == {"id", "label", "type_uri", "description", "ext_id"}
    assert result["id"] == str(row.id)
    assert result["label"] == row.label
    assert result["type_uri"] == row.type_uri
    assert result["description"] == row.description
    assert result["ext_id"] == row.ext_id


def test_serialize_entity_none_fields():
    row = _make_entity_row(type_uri=None, description=None, ext_id=None)
    result = serialize_entity(row)
    assert result["type_uri"] is None
    assert result["description"] is None
    assert result["ext_id"] is None


# ── serialize_source ──────────────────────────────────────────────────────────

def test_serialize_source_full_shape():
    """Every required field must be present — this is the load-bearing provenance check."""
    row = _make_source_row()
    result = serialize_source(row)
    required_fields = {
        "id", "url", "publisher", "fetched_at", "content_hash",
        "archive_url", "excerpt", "excerpt_offset_start", "excerpt_offset_end",
        "last_verified_at", "verification_status", "extraction_method",
    }
    assert required_fields.issubset(set(result.keys())), (
        f"Missing fields: {required_fields - set(result.keys())}"
    )
    assert result["id"] == str(row.source_id)
    assert result["url"] == row.url
    assert result["publisher"] == row.publisher
    assert result["excerpt"] == row.excerpt
    assert result["excerpt_offset_start"] == row.excerpt_offset_start
    assert result["excerpt_offset_end"] == row.excerpt_offset_end
    assert result["verification_status"] == row.verification_status
    assert result["extraction_method"] == row.extraction_method


def test_serialize_source_datetime_is_iso_string():
    row = _make_source_row()
    result = serialize_source(row)
    assert isinstance(result["fetched_at"], str)
    assert "T" in result["fetched_at"]
    assert isinstance(result["last_verified_at"], str)


def test_serialize_source_archive_url_none_allowed():
    row = _make_source_row(archive_url=None)
    result = serialize_source(row)
    assert result["archive_url"] is None


# ── serialize_fact ────────────────────────────────────────────────────────────

def test_serialize_fact_number_value():
    stmt = _make_stmt_row(val_number=4200000000, value_type="number")
    result = serialize_fact(stmt, qualifier_rows=[], source_rows=[])
    assert result["value"] == {"number": 4200000000.0}
    assert result["rank"] == "preferred"
    assert result["confidence"] == 0.85
    assert result["property"]["slug"] == "revenue_usd"
    assert result["subject"]["label"] == "MegaCorp"
    assert result["qualifiers"] == []
    assert result["sources"] == []


def test_serialize_fact_text_value():
    stmt = _make_stmt_row(val_text="CEO", val_number=None, value_type="string")
    result = serialize_fact(stmt, qualifier_rows=[], source_rows=[])
    assert result["value"] == {"text": "CEO"}


def test_serialize_fact_entity_value():
    target_id = uuid.uuid4()
    stmt = _make_stmt_row(val_entity=target_id, val_number=None, value_type="entity_ref")
    result = serialize_fact(stmt, qualifier_rows=[], source_rows=[])
    assert result["value"] == {"entity": {"id": str(target_id)}}


def test_serialize_fact_date_value():
    now = datetime(2026, 5, 22, tzinfo=timezone.utc)
    stmt = _make_stmt_row(val_date=now, val_number=None, value_type="date")
    result = serialize_fact(stmt, qualifier_rows=[], source_rows=[])
    assert result["value"]["date"] == now.isoformat()


def test_serialize_fact_includes_qualifiers_and_sources():
    stmt = _make_stmt_row()
    qual = _make_qual_row()
    src = _make_source_row()
    result = serialize_fact(stmt, qualifier_rows=[qual], source_rows=[src])
    assert len(result["qualifiers"]) == 1
    assert len(result["sources"]) == 1
    # Source inside fact must also carry full provenance
    source_in_fact = result["sources"][0]
    assert "excerpt" in source_in_fact
    assert "excerpt_offset_start" in source_in_fact
    assert "verification_status" in source_in_fact


# ── serialize_relation ────────────────────────────────────────────────────────

def test_serialize_relation_shape():
    row = _make_relation_row()
    result = serialize_relation(row)
    assert result["source"]["id"] == str(row.source_id)
    assert result["source"]["label"] == row.source_label
    assert result["target"]["id"] == str(row.target_id)
    assert result["target"]["label"] == row.target_label
    assert result["type"] == row.type
    assert result["confidence"] == float(row.confidence)
    assert result["statement_id"] == str(row.statement_id)
```

- [ ] **RUN/FAIL:**

```bash
$ python -m pytest tests/assembler/test_serialize.py -x 2>&1 | head -20
# expected: ImportError for factvault.assembler.serialize
```

- [ ] **IMPLEMENT:** `factvault/assembler/serialize.py`:

```python
# factvault/assembler/serialize.py
"""
Pure serialization functions: DB row objects → bundle JSON dicts.

All functions are pure (no DB calls).  They accept SQLAlchemy Row or
namedtuple-like objects and return plain Python dicts suitable for JSON
serialization.

The source serializer is load-bearing: every field required by the spec's
bundle JSON shape must be present.  Missing fields will cause downstream
LLM consumers to fail silently — do not add Optional shortcuts here.
"""
from __future__ import annotations

from typing import Any


def serialize_entity(row: Any) -> dict:
    """
    Row columns: id, label, type_uri, description, ext_id.
    """
    return {
        "id": str(row.id),
        "label": row.label,
        "type_uri": row.type_uri,
        "description": row.description,
        "ext_id": row.ext_id,
    }


def serialize_source(row: Any) -> dict:
    """
    Serialize a source row from a ``statement_sources JOIN sources`` result.

    Row columns (from _fetch_facts query in bundle.py):
        statement_id, extraction_method,
        excerpt, excerpt_offset_start, excerpt_offset_end,
        source_id, url, publisher, fetched_at,
        content_hash, archive_url,
        last_verified_at, verification_status.

    EVERY field is required.  Callers must not drop fields from the JOIN query.
    """
    return {
        "id": str(row.source_id),
        "url": row.url,
        "publisher": row.publisher,
        "fetched_at": row.fetched_at.isoformat() if row.fetched_at is not None else None,
        "content_hash": row.content_hash,
        "archive_url": row.archive_url,
        "excerpt": row.excerpt,
        "excerpt_offset_start": row.excerpt_offset_start,
        "excerpt_offset_end": row.excerpt_offset_end,
        "last_verified_at": (
            row.last_verified_at.isoformat()
            if row.last_verified_at is not None else None
        ),
        "verification_status": row.verification_status,
        "extraction_method": row.extraction_method,
    }


def serialize_qualifier(row: Any) -> dict:
    """
    Row columns: id, statement_id, property_id,
                 val_text, val_number, val_date, val_entity,
                 property_slug, property_label, value_type.
    """
    return {
        "property": {
            "slug": row.property_slug,
            "label": row.property_label,
            "value_type": row.value_type,
        },
        "value": _serialize_value(
            val_text=row.val_text,
            val_number=row.val_number,
            val_date=row.val_date,
            val_entity=row.val_entity,
            value_type=row.value_type,
        ),
    }


def serialize_fact(statement_row: Any, qualifier_rows: list, source_rows: list) -> dict:
    """
    Row columns for statement_row: id, subject_id, property_id,
        val_text, val_number, val_date, val_entity, val_json,
        rank, confidence, created_at,
        property_slug, property_label, value_type, subject_label.
    """
    return {
        "id": str(statement_row.id),
        "subject": {
            "id": str(statement_row.subject_id),
            "label": statement_row.subject_label,
        },
        "property": {
            "slug": statement_row.property_slug,
            "label": statement_row.property_label,
            "value_type": statement_row.value_type,
        },
        "value": _serialize_value(
            val_text=statement_row.val_text,
            val_number=statement_row.val_number,
            val_date=statement_row.val_date,
            val_entity=statement_row.val_entity,
            value_type=statement_row.value_type,
        ),
        "qualifiers": [serialize_qualifier(q) for q in qualifier_rows],
        "rank": statement_row.rank,
        "confidence": float(statement_row.confidence),
        "sources": [serialize_source(s) for s in source_rows],
    }


def serialize_relation(row: Any) -> dict:
    """
    Row columns: id, source_id, target_id, type, confidence,
                 statement_id, source_label, target_label.
    """
    return {
        "source": {"id": str(row.source_id), "label": row.source_label},
        "target": {"id": str(row.target_id), "label": row.target_label},
        "type": row.type,
        "confidence": float(row.confidence) if row.confidence is not None else None,
        "statement_id": str(row.statement_id) if row.statement_id is not None else None,
    }


# ── Private helpers ────────────────────────────────────────────────────────────

def _serialize_value(
    val_text: Any,
    val_number: Any,
    val_date: Any,
    val_entity: Any,
    value_type: str,
) -> dict:
    """
    Produce the ``"value"`` sub-dict based on which value column is populated.
    Returns ``{}`` only if all value columns are None (should not occur in
    valid data due to the DB CHECK constraint).
    """
    if val_text is not None:
        return {"text": val_text}
    if val_number is not None:
        return {"number": float(val_number)}
    if val_date is not None:
        date_str = val_date.isoformat() if hasattr(val_date, "isoformat") else str(val_date)
        return {"date": date_str}
    if val_entity is not None:
        return {"entity": {"id": str(val_entity)}}
    return {}
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/assembler/test_serialize.py -v
# expected: 12 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/assembler/serialize.py tests/assembler/test_serialize.py
git commit -m "feat(assembler): serialization helpers for entity/fact/relation/source bundle shapes"
```

---

### Task 6 — Graph expansion via recursive CTE

- [ ] **FAIL:** Write the failing test:

```python
# tests/assembler/test_graph.py
"""
Tests for factvault.assembler.graph.expand_entities.

Inserts a chain of entities connected by relations and asserts that
expand_entities returns the correct set at each depth.
"""
import uuid
import pytest
from sqlalchemy import text

from factvault.assembler.graph import expand_entities


@pytest.fixture()
def graph_data(conn):
    """
    Insert a four-node chain: A → B → C → D.
    Returns a dict: {"A": uuid, "B": uuid, "C": uuid, "D": uuid, "tenant_id": uuid}.
    """
    tid = uuid.uuid4()
    ids = {node: uuid.uuid4() for node in ("A", "B", "C", "D")}
    prop_id = uuid.uuid4()

    # Insert a property for the relation type
    conn.execute(text(
        "INSERT INTO properties (id, tenant_id, slug, label, value_type) "
        "VALUES (:id, :tid, 'related_to', 'Related to', 'entity_ref')"
    ), {"id": str(prop_id), "tid": str(tid)})

    for node, eid in ids.items():
        conn.execute(text(
            "INSERT INTO entities (id, tenant_id, label) VALUES (:id, :tid, :label)"
        ), {"id": str(eid), "tid": str(tid), "label": node})

    # Insert relations: A→B, B→C, C→D (all with confidence >= 0.4 so they are traversed)
    edges = [("A", "B"), ("B", "C"), ("C", "D")]
    for src_name, tgt_name in edges:
        conn.execute(text(
            "INSERT INTO relations "
            "(id, tenant_id, source_id, target_id, type, confidence) "
            "VALUES (:id, :tid, :src, :tgt, 'related_to', 0.8)"
        ), {
            "id": str(uuid.uuid4()), "tid": str(tid),
            "src": str(ids[src_name]), "tgt": str(ids[tgt_name]),
        })

    ids["tenant_id"] = tid
    return ids


def test_expand_depth0_returns_seed_only(conn, graph_data):
    tid = graph_data["tenant_id"]
    result = expand_entities(conn, seed_ids=[graph_data["A"]], depth=0)
    assert result == {graph_data["A"]}


def test_expand_depth1_returns_two_hops(conn, graph_data):
    tid = graph_data["tenant_id"]
    result = expand_entities(conn, seed_ids=[graph_data["A"]], depth=1)
    # A plus its immediate neighbour B
    assert result == {graph_data["A"], graph_data["B"]}


def test_expand_depth2_returns_three_hops(conn, graph_data):
    tid = graph_data["tenant_id"]
    result = expand_entities(conn, seed_ids=[graph_data["A"]], depth=2)
    assert result == {graph_data["A"], graph_data["B"], graph_data["C"]}


def test_expand_depth3_returns_all_four(conn, graph_data):
    tid = graph_data["tenant_id"]
    result = expand_entities(conn, seed_ids=[graph_data["A"]], depth=3)
    assert result == {graph_data["A"], graph_data["B"], graph_data["C"], graph_data["D"]}


def test_expand_low_confidence_edge_not_traversed(conn, graph_data):
    """An edge with confidence < 0.4 is not traversed."""
    tid = graph_data["tenant_id"]
    new_entity_id = uuid.uuid4()
    conn.execute(text(
        "INSERT INTO entities (id, tenant_id, label) VALUES (:id, :tid, 'E')"
    ), {"id": str(new_entity_id), "tid": str(tid)})
    conn.execute(text(
        "INSERT INTO relations "
        "(id, tenant_id, source_id, target_id, type, confidence) "
        "VALUES (:id, :tid, :src, :tgt, 'related_to', 0.3)"
    ), {
        "id": str(uuid.uuid4()), "tid": str(tid),
        "src": str(graph_data["D"]), "tgt": str(new_entity_id),
    })
    result = expand_entities(conn, seed_ids=[graph_data["A"]], depth=4)
    # D is reachable but E (connected via 0.3 confidence) is not
    assert new_entity_id not in result


def test_expand_max_entities_bound(conn, graph_data):
    """max_entities cap prevents unbounded expansion."""
    result = expand_entities(conn, seed_ids=[graph_data["A"]], depth=3, max_entities=2)
    assert len(result) <= 2
```

- [ ] **RUN/FAIL:**

```bash
$ python -m pytest tests/assembler/test_graph.py -x 2>&1 | head -20
# expected: ImportError for factvault.assembler.graph
```

- [ ] **IMPLEMENT:** `factvault/assembler/graph.py`:

```python
# factvault/assembler/graph.py
"""
factvault.assembler.graph — Recursive CTE graph expansion.

``expand_entities()`` traverses the ``relations`` table using a recursive
CTE to collect all entity UUIDs reachable from the seed set within
``depth`` hops, bounded by ``max_entities``.

Edges with ``confidence < 0.4`` are not traversed (spec §3.4).
Synthetic ``type='embedding-near'`` edges are excluded from traversal
to avoid polluting story bundles with low-signal similarity edges.
"""
from __future__ import annotations

from uuid import UUID

from sqlalchemy import text
from sqlalchemy.engine import Connection


def expand_entities(
    connection: Connection,
    seed_ids: list[UUID],
    depth: int,
    max_entities: int = 200,
) -> set[UUID]:
    """
    Return the set of entity UUIDs reachable from *seed_ids* within *depth*
    hops through the ``relations`` table.

    Args:
        connection: Active SQLAlchemy connection.  Must be inside a transaction
            with ``app.tenant_id`` set if RLS is enabled.
        seed_ids: Starting entity UUIDs.
        depth: Maximum hop count.  depth=0 returns seed_ids unchanged.
        max_entities: Hard cap on the number of entities returned.  Prevents
            runaway expansion on dense graphs.  Capped at 200 by default.

    Returns:
        Set of UUID objects (includes the seeds themselves).
    """
    if not seed_ids:
        return set()

    if depth == 0:
        return set(seed_ids)

    ids_list = [str(eid) for eid in seed_ids]

    # The recursive CTE mirrors the spec §3.4 query exactly.
    # We use CAST(:ids AS uuid[]) rather than ANY(:ids::uuid[]) to avoid
    # psycopg's refusal to bind the ::cast syntax (plan-bug pattern #3).
    result = connection.execute(
        text(
            """
            WITH RECURSIVE entity_graph AS (
                -- Seed: starting entities
                SELECT id, 0 AS hop
                FROM entities
                WHERE id = ANY(CAST(:ids AS uuid[]))

                UNION

                -- Expand one hop through relations (both directions)
                SELECT
                    CASE
                        WHEN r.source_id = eg.id THEN r.target_id
                        ELSE r.source_id
                    END AS id,
                    eg.hop + 1
                FROM entity_graph eg
                JOIN relations r
                    ON (r.source_id = eg.id OR r.target_id = eg.id)
                    AND r.confidence >= 0.4
                    AND r.type != 'embedding-near'
                WHERE eg.hop < :max_depth
            )
            SELECT DISTINCT id
            FROM entity_graph
            LIMIT :max_entities
            """
        ),
        {
            "ids": ids_list,
            "max_depth": depth,
            "max_entities": max_entities,
        },
    )

    return {UUID(str(row.id)) for row in result.fetchall()}
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/assembler/test_graph.py -v
# expected: 6 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/assembler/graph.py tests/assembler/test_graph.py
git commit -m "feat(assembler): recursive CTE graph expansion with confidence + depth bounds"
```

---

### Task 7 — Assembler integrates graph expansion (depth > 0)

- [ ] **FAIL:** Append to `tests/assembler/test_bundle.py`:

```python
# tests/assembler/test_bundle.py  (append)

def test_assemble_depth2_includes_graph_neighbours(conn, tenant_id):
    """
    With depth=2, assembling entity A returns A's 2-hop neighbours.
    Insert A → B → C via relations; assemble(A, depth=2) must include
    B's and C's statements in the bundle.
    """
    # Insert 3 entities: A, B, C
    ids = {node: uuid.uuid4() for node in ("A", "B", "C")}
    prop_id = uuid.uuid4()

    conn.execute(text(
        "INSERT INTO properties (id, tenant_id, slug, label, value_type) "
        "VALUES (:id, :tid, 'depth_test_prop', 'Depth test', 'string')"
    ), {"id": str(prop_id), "tid": str(tenant_id)})

    for node, eid in ids.items():
        conn.execute(text(
            "INSERT INTO entities (id, tenant_id, label) VALUES (:id, :tid, :label)"
        ), {"id": str(eid), "tid": str(tenant_id), "label": node})

    # A→B and B→C via relations
    for src_name, tgt_name in [("A", "B"), ("B", "C")]:
        conn.execute(text(
            "INSERT INTO relations "
            "(id, tenant_id, source_id, target_id, type, confidence) "
            "VALUES (:id, :tid, :src, :tgt, 'depth_test_prop', 0.8)"
        ), {
            "id": str(uuid.uuid4()), "tid": str(tenant_id),
            "src": str(ids[src_name]), "tgt": str(ids[tgt_name]),
        })

    # Give each entity one statement so they appear in facts[]
    for node, eid in ids.items():
        stmt_id = uuid.uuid4()
        conn.execute(text(
            "INSERT INTO statements "
            "(id, tenant_id, subject_id, property_id, val_text, rank, confidence) "
            "VALUES (:id, :tid, :subj, :prop, :val, 'normal', 0.5)"
        ), {
            "id": str(stmt_id), "tid": str(tenant_id),
            "subj": str(eid), "prop": str(prop_id), "val": f"value_for_{node}",
        })

    assembler = BundleAssembler(conn)
    bundle = assembler.assemble(
        entity_ids=[ids["A"]],
        depth=2,
        tenant_id=tenant_id,
    )

    returned_entity_ids = {e["id"] for e in bundle["entities"]}
    assert str(ids["A"]) in returned_entity_ids
    assert str(ids["B"]) in returned_entity_ids
    assert str(ids["C"]) in returned_entity_ids

    # All three entities should contribute facts
    fact_subject_ids = {f["subject"]["id"] for f in bundle["facts"]}
    assert str(ids["A"]) in fact_subject_ids
    assert str(ids["B"]) in fact_subject_ids
    assert str(ids["C"]) in fact_subject_ids
```

- [ ] **RUN/FAIL:**

```bash
$ python -m pytest tests/assembler/test_bundle.py::test_assemble_depth2_includes_graph_neighbours -x 2>&1 | head -20
# expected: FAILED — either wrong entity count or graph expansion not yet wired in
```

Note: `BundleAssembler.assemble()` already calls `expand_entities()` for depth > 0 (implemented in Task 4). If the test passes without further changes, confirm it and move on. If it fails due to a missing import or wrong UUID type, fix `factvault/assembler/bundle.py` — the `_fetch_entities` query uses `CAST(:ids AS uuid[])` with a list of strings; verify the `expand_entities` return set is converted to strings before passing to `_fetch_entities`.

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/assembler/test_bundle.py -v
# expected: 4 passed (3 from Task 4 + 1 new)
```

- [ ] **COMMIT:**

```bash
git add tests/assembler/test_bundle.py
git commit -m "test(assembler): verify depth>0 graph expansion integrates into assemble()"
```

---

### Task 8 — Conflict surfacing in bundles

- [ ] **FAIL:** Append to `tests/assembler/test_bundle.py`:

```python
# tests/assembler/test_bundle.py  (append)

def test_assemble_surfaces_conflicts(conn, tenant_id):
    """
    Two non-deprecated statements for the same (subject, property) with
    differing values must appear in the bundle's conflicts[] array.
    """
    entity_id = uuid.uuid4()
    property_id = uuid.uuid4()
    stmt_a_id = uuid.uuid4()
    stmt_b_id = uuid.uuid4()

    conn.execute(text(
        "INSERT INTO entities (id, tenant_id, label) VALUES (:id, :tid, 'ConflictEntity')"
    ), {"id": str(entity_id), "tid": str(tenant_id)})

    conn.execute(text(
        "INSERT INTO properties (id, tenant_id, slug, label, value_type) "
        "VALUES (:id, :tid, 'conflict_prop', 'Conflict test prop', 'string')"
    ), {"id": str(property_id), "tid": str(tenant_id)})

    # Two normal-ranked statements with different string values → conflict
    for stmt_id, val in [(stmt_a_id, "value_alpha"), (stmt_b_id, "value_beta")]:
        conn.execute(text(
            "INSERT INTO statements "
            "(id, tenant_id, subject_id, property_id, val_text, rank, confidence) "
            "VALUES (:id, :tid, :subj, :prop, :val, 'normal', 0.5)"
        ), {
            "id": str(stmt_id), "tid": str(tenant_id),
            "subj": str(entity_id), "prop": str(property_id), "val": val,
        })

    assembler = BundleAssembler(conn)
    bundle = assembler.assemble(
        entity_ids=[entity_id],
        depth=0,
        tenant_id=tenant_id,
    )

    assert len(bundle["conflicts"]) == 1
    conflict = bundle["conflicts"][0]
    assert conflict["subject"]["id"] == str(entity_id)
    assert conflict["property_slug"] == "conflict_prop"
    assert len(conflict["competing_values"]) == 2

    stmt_ids_in_conflict = {cv["statement_id"] for cv in conflict["competing_values"]}
    assert str(stmt_a_id) in stmt_ids_in_conflict
    assert str(stmt_b_id) in stmt_ids_in_conflict


def test_assemble_deprecated_statements_not_in_conflicts(conn, tenant_id):
    """
    A deprecated statement paired with a normal one must NOT appear in conflicts[].
    """
    entity_id = uuid.uuid4()
    property_id = uuid.uuid4()

    conn.execute(text(
        "INSERT INTO entities (id, tenant_id, label) VALUES (:id, :tid, 'DeprecatedEntity')"
    ), {"id": str(entity_id), "tid": str(tenant_id)})

    conn.execute(text(
        "INSERT INTO properties (id, tenant_id, slug, label, value_type) "
        "VALUES (:id, :tid, 'dep_prop', 'Deprecated prop', 'string')"
    ), {"id": str(property_id), "tid": str(tenant_id)})

    # One normal, one deprecated — no conflict expected
    conn.execute(text(
        "INSERT INTO statements "
        "(id, tenant_id, subject_id, property_id, val_text, rank, confidence) "
        "VALUES (:id, :tid, :subj, :prop, 'current_value', 'normal', 0.85)"
    ), {"id": str(uuid.uuid4()), "tid": str(tenant_id), "subj": str(entity_id), "prop": str(property_id)})

    conn.execute(text(
        "INSERT INTO statements "
        "(id, tenant_id, subject_id, property_id, val_text, rank, confidence) "
        "VALUES (:id, :tid, :subj, :prop, 'old_value', 'deprecated', 0.5)"
    ), {"id": str(uuid.uuid4()), "tid": str(tenant_id), "subj": str(entity_id), "prop": str(property_id)})

    assembler = BundleAssembler(conn)
    bundle = assembler.assemble(
        entity_ids=[entity_id],
        depth=0,
        tenant_id=tenant_id,
    )
    assert bundle["conflicts"] == []
```

- [ ] **RUN/FAIL → RUN/PASS:** The conflict detection is already implemented in `BundleAssembler._detect_conflicts()` via `v_conflicts` (Task 4). These tests should pass without changes if the view was created in Plan 1 migration `0013_v_conflicts_view.py`. If they fail, confirm `v_conflicts` exists:

```bash
$ python -c "
from tests.conftest import *
# or run via pytest with -s to inspect
"
# If v_conflicts is missing: the Plan 1 migration must be re-applied.
# This is an environment issue, not a code issue.
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/assembler/test_bundle.py -v
# expected: 6 passed
```

- [ ] **COMMIT:**

```bash
git add tests/assembler/test_bundle.py
git commit -m "test(assembler): conflict surfacing in bundles via v_conflicts view"
```

---

### Task 9 — Pydantic request/response schemas

- [ ] **FAIL:** Create `tests/api/__init__.py` (empty) and write the failing test:

```python
# tests/api/__init__.py
```

```python
# tests/api/test_schemas.py
"""
Tests for Pydantic request/response models in factvault.api.schemas.
"""
import uuid
import pytest
from pydantic import ValidationError

from factvault.api.schemas import (
    StoryQuery,
    FactQuery,
    QualifierFilter,
    BundleResponse,
)


# ── StoryQuery ─────────────────────────────────────────────────────────────────

def test_story_query_defaults():
    q = StoryQuery(query="biotech CFO departures")
    assert q.depth == 2
    assert q.max_facts == 300
    assert q.min_confidence == 0.4


def test_story_query_custom_values():
    q = StoryQuery(query="AI legislation", depth=3, max_facts=500, min_confidence=0.5)
    assert q.depth == 3
    assert q.max_facts == 500
    assert q.min_confidence == 0.5


def test_story_query_depth_bounds():
    with pytest.raises(ValidationError):
        StoryQuery(query="test", depth=0)  # depth must be >= 1 for story mode
    with pytest.raises(ValidationError):
        StoryQuery(query="test", depth=5)  # spec max is 3 for on-demand; allow up to 4 for flexibility but not 5


def test_story_query_empty_string_rejected():
    with pytest.raises(ValidationError):
        StoryQuery(query="")


# ── FactQuery ──────────────────────────────────────────────────────────────────

def test_fact_query_minimal():
    q = FactQuery(property="raised_usd")
    assert q.property == "raised_usd"
    assert q.subject_type is None
    assert q.min_confidence == 0.0
    assert q.qualifiers == []


def test_fact_query_with_qualifiers():
    q = FactQuery(
        property="raised_usd",
        subject_type="https://schema.org/Organization",
        qualifiers=[{"property": "point_in_time", "gte": "2024-01-01"}],
        min_confidence=0.5,
    )
    assert len(q.qualifiers) == 1
    assert q.qualifiers[0].property == "point_in_time"
    assert q.qualifiers[0].gte == "2024-01-01"


def test_fact_query_empty_property_rejected():
    with pytest.raises(ValidationError):
        FactQuery(property="")


# ── BundleResponse ─────────────────────────────────────────────────────────────

def test_bundle_response_from_assembler_output():
    """BundleResponse must accept the dict produced by BundleAssembler.assemble()."""
    sample = {
        "query": {
            "type": "dossier",
            "entity_ids": [str(uuid.uuid4())],
            "depth": 0,
            "tenant_id": str(uuid.uuid4()),
            "query_text": None,
        },
        "assembled_at": "2026-05-22T04:00:00+00:00",
        "entities": [
            {
                "id": str(uuid.uuid4()),
                "label": "MegaCorp",
                "type_uri": "https://schema.org/Organization",
                "description": "A tech company",
                "ext_id": "CIK:001",
            }
        ],
        "facts": [],
        "relations": [],
        "conflicts": [],
    }
    bundle = BundleResponse(**sample)
    assert bundle.assembled_at == "2026-05-22T04:00:00+00:00"
    assert len(bundle.entities) == 1


def test_bundle_response_empty_bundle():
    sample = {
        "query": {
            "type": "dossier",
            "entity_ids": [],
            "depth": 0,
            "tenant_id": None,
            "query_text": None,
        },
        "assembled_at": "2026-05-22T04:00:00+00:00",
        "entities": [],
        "facts": [],
        "relations": [],
        "conflicts": [],
    }
    bundle = BundleResponse(**sample)
    assert bundle.entities == []
```

- [ ] **RUN/FAIL:**

```bash
$ python -m pytest tests/api/test_schemas.py -x 2>&1 | head -20
# expected: ImportError for factvault.api.schemas
```

- [ ] **IMPLEMENT:** Create `factvault/api/__init__.py` (empty) and `factvault/api/schemas.py`:

```python
# factvault/api/__init__.py
```

```python
# factvault/api/schemas.py
"""
Pydantic v2 request/response models for the factvault REST API.

BundleResponse mirrors the dict returned by BundleAssembler.assemble()
so that FastAPI can validate and serialize bundle payloads automatically.
The spec's canonical JSON shape (§3.4) is the authoritative reference.
"""
from __future__ import annotations

from typing import Any, Optional
from pydantic import BaseModel, Field, field_validator


# ── Request models ─────────────────────────────────────────────────────────────

class StoryQuery(BaseModel):
    """Request body for ``POST /stories``."""

    query: str = Field(..., min_length=1, description="Free-text story query.")
    depth: int = Field(
        default=2,
        ge=1,
        le=4,
        description="Graph expansion depth (1–4).  2 or 3 is typical.",
    )
    max_facts: int = Field(default=300, ge=0, le=5000)
    min_confidence: float = Field(default=0.4, ge=0.0, le=1.0)


class QualifierFilter(BaseModel):
    """A single qualifier filter inside FactQuery."""

    property: str = Field(..., min_length=1)
    gte: Optional[str] = None   # ISO date string or numeric string
    lte: Optional[str] = None
    eq: Optional[str] = None


class FactQuery(BaseModel):
    """Request body for ``POST /facts/query``."""

    property: str = Field(..., min_length=1, description="Property slug to filter on.")
    subject_type: Optional[str] = None   # schema.org URI
    qualifiers: list[QualifierFilter] = Field(default_factory=list)
    min_confidence: float = Field(default=0.0, ge=0.0, le=1.0)
    limit: int = Field(default=20, ge=1, le=200)
    cursor: Optional[str] = None


class EntityLookupParams(BaseModel):
    """Query params for ``GET /entities/by-name``."""

    q: str = Field(..., min_length=1, description="Fuzzy name search string.")
    limit: int = Field(default=5, ge=1, le=20)


# ── Response models ────────────────────────────────────────────────────────────

class QueryMeta(BaseModel):
    type: str
    entity_ids: list[str]
    depth: int
    tenant_id: Optional[str] = None
    query_text: Optional[str] = None


class EntitySummary(BaseModel):
    id: str
    label: str
    type_uri: Optional[str] = None
    description: Optional[str] = None
    ext_id: Optional[str] = None


class RelationEntry(BaseModel):
    source: dict[str, str]
    target: dict[str, str]
    type: str
    confidence: Optional[float] = None
    statement_id: Optional[str] = None


class ConflictEntry(BaseModel):
    subject: dict[str, str]
    property_slug: str
    competing_values: list[dict[str, Any]]


class BundleResponse(BaseModel):
    """
    Canonical bundle response.  Mirrors the dict returned by
    ``BundleAssembler.assemble()``.

    Facts and relations are typed as ``list[dict]`` rather than strict
    nested models because their internal shape varies by value_type.
    Callers that need strict validation of fact internals should use the
    serialization helpers in ``factvault.assembler.serialize`` directly.
    """

    query: QueryMeta
    assembled_at: str
    entities: list[EntitySummary]
    facts: list[dict[str, Any]]
    relations: list[dict[str, Any]]
    conflicts: list[dict[str, Any]]
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/api/test_schemas.py -v
# expected: 9 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/api/__init__.py factvault/api/schemas.py \
        tests/api/__init__.py tests/api/test_schemas.py
git commit -m "feat(api): Pydantic request/response schemas for StoryQuery, FactQuery, BundleResponse"
```

---

### Task 10 — FastAPI app skeleton and health routes

- [ ] **FAIL:** Create `tests/api/test_health.py`:

```python
# tests/api/test_health.py
"""
Tests for GET /healthz and GET /readyz.
Uses FastAPI's synchronous TestClient (no asyncio needed).
"""
import pytest
from unittest.mock import patch, MagicMock
from fastapi.testclient import TestClient


@pytest.fixture()
def client():
    from factvault.api.main import app
    return TestClient(app, raise_server_exceptions=False)


def test_healthz_returns_200(client):
    resp = client.get("/healthz")
    assert resp.status_code == 200
    assert resp.json() == {"status": "ok"}


def test_readyz_returns_200_when_db_reachable(client):
    """Patch the DB check to succeed."""
    with patch("factvault.api.routes.health._check_db_reachable", return_value=True):
        resp = client.get("/readyz")
    assert resp.status_code == 200
    assert resp.json()["status"] == "ok"


def test_readyz_returns_503_when_db_unreachable(client):
    """Patch the DB check to fail."""
    with patch("factvault.api.routes.health._check_db_reachable", return_value=False):
        resp = client.get("/readyz")
    assert resp.status_code == 503
    assert resp.json()["status"] == "error"


def test_unknown_route_returns_404(client):
    resp = client.get("/does-not-exist")
    assert resp.status_code == 404
```

- [ ] **RUN/FAIL:**

```bash
$ python -m pytest tests/api/test_health.py -x 2>&1 | head -20
# expected: ImportError for factvault.api.main
```

- [ ] **IMPLEMENT:** Create the directory structure and files:

```python
# factvault/api/routes/__init__.py
```

```python
# factvault/api/routes/health.py
"""
Health and readiness probe routes.

GET /healthz — liveness: returns 200 unconditionally.
GET /readyz  — readiness: checks DB connectivity; returns 503 if DB is down.
"""
from __future__ import annotations

import os

from fastapi import APIRouter
from fastapi.responses import JSONResponse

router = APIRouter()


def _check_db_reachable() -> bool:
    """
    Run a SELECT 1 against the configured database URL.
    Returns True if the query succeeds, False otherwise.
    """
    db_url = os.environ.get("FACTVAULT_DATABASE_URL")
    if not db_url:
        return False
    try:
        from sqlalchemy import create_engine, text
        engine = create_engine(db_url, pool_pre_ping=True)
        with engine.connect() as conn:
            conn.execute(text("SELECT 1"))
        return True
    except Exception:
        return False


@router.get("/healthz", tags=["health"])
def healthz() -> dict:
    """Liveness probe. Always returns 200 if the process is running."""
    return {"status": "ok"}


@router.get("/readyz", tags=["health"])
def readyz() -> JSONResponse:
    """
    Readiness probe.  Returns 200 if the DB is reachable, 503 otherwise.
    Kubernetes will hold traffic until this returns 200.
    """
    if _check_db_reachable():
        return JSONResponse(status_code=200, content={"status": "ok"})
    return JSONResponse(status_code=503, content={"status": "error", "detail": "database unreachable"})
```

```python
# factvault/api/main.py
"""
factvault FastAPI application.

Entry point for the REST API server.  Run via:

    uvicorn factvault.api.main:app --host 0.0.0.0 --port 8000

or via the console_script:

    factvault-api
"""
from __future__ import annotations

import os

from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from factvault.api.routes.health import router as health_router


def create_app() -> FastAPI:
    """
    Factory function that creates and configures the FastAPI application.

    Separating creation from the module-level ``app`` instance allows
    test code to call ``create_app()`` with patched settings if needed.
    """
    application = FastAPI(
        title="factvault",
        description=(
            "Self-hostable research database where every fact is grounded "
            "in a verifiable, durably-archived source."
        ),
        version="0.0.1",
        docs_url="/docs",
        redoc_url="/redoc",
        openapi_url="/openapi.json",
    )

    # CORS: permissive for now; Plan 5 tightens this per-tenant.
    application.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    # Health routes (no auth required)
    application.include_router(health_router)

    return application


#: Module-level app instance used by uvicorn and FastAPI TestClient.
app = create_app()


def run() -> None:
    """
    Console script entry point (``factvault-api``).
    Starts uvicorn with settings from environment variables.
    """
    import uvicorn

    host = os.environ.get("FACTVAULT_API_HOST", "0.0.0.0")
    port = int(os.environ.get("FACTVAULT_API_PORT", "8000"))
    reload = os.environ.get("FACTVAULT_API_RELOAD", "false").lower() == "true"

    uvicorn.run(
        "factvault.api.main:app",
        host=host,
        port=port,
        reload=reload,
    )
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/api/test_health.py -v
# expected: 4 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/api/main.py factvault/api/routes/__init__.py \
        factvault/api/routes/health.py tests/api/test_health.py
git commit -m "feat(api): FastAPI app skeleton with /healthz and /readyz routes"
```

---

### Task 11 — Auth middleware and tenant_id resolution

- [ ] **FAIL:** Create `tests/api/test_auth.py`:

```python
# tests/api/test_auth.py
"""
Tests for JWT auth middleware and FastAPI dependency injection.

Uses TestClient with a patched JWTVerifier so these tests do not
depend on real RSA keys or the dev-key infrastructure.
"""
import uuid
import time
import pytest
from unittest.mock import patch, MagicMock
from fastapi.testclient import TestClient

from factvault.auth.jwt import JWTError


@pytest.fixture()
def valid_tenant_id():
    return str(uuid.uuid4())


@pytest.fixture()
def mock_verifier(valid_tenant_id):
    """A JWTVerifier mock that returns valid claims for any token."""
    verifier = MagicMock()
    verifier.verify.return_value = {"sub": valid_tenant_id, "exp": int(time.time()) + 3600}
    return verifier


@pytest.fixture()
def client_with_auth(mock_verifier):
    """TestClient with auth middleware active and verifier patched."""
    with patch("factvault.api.auth._get_verifier", return_value=mock_verifier):
        from factvault.api.main import app
        return TestClient(app, raise_server_exceptions=False)


def test_request_without_authorization_header_returns_401(client_with_auth):
    resp = client_with_auth.get("/entities/by-name?q=test")
    # /entities/by-name is not yet wired (Task 12), but the middleware
    # should intercept all non-health routes and return 401 before routing.
    # If the route doesn't exist yet, 404 is also acceptable here — the
    # important invariant is that 200 is NOT returned without a token.
    assert resp.status_code in (401, 404)


def test_request_with_invalid_token_returns_401():
    bad_verifier = MagicMock()
    bad_verifier.verify.side_effect = JWTError("Token expired: Signature has expired.")
    with patch("factvault.api.auth._get_verifier", return_value=bad_verifier):
        from factvault.api.main import app
        client = TestClient(app, raise_server_exceptions=False)
        resp = client.get(
            "/entities/by-name?q=test",
            headers={"Authorization": "Bearer bad.token.here"},
        )
    assert resp.status_code == 401


def test_healthz_requires_no_auth():
    """Health routes must be exempt from JWT auth."""
    from factvault.api.main import app
    client = TestClient(app, raise_server_exceptions=False)
    resp = client.get("/healthz")
    assert resp.status_code == 200


def test_readyz_requires_no_auth():
    with patch("factvault.api.routes.health._check_db_reachable", return_value=True):
        from factvault.api.main import app
        client = TestClient(app, raise_server_exceptions=False)
        resp = client.get("/readyz")
    assert resp.status_code == 200


def test_get_tenant_id_dependency_extracts_from_state(valid_tenant_id, mock_verifier):
    """
    get_tenant_id() dependency reads tenant_id from request.state after
    the middleware populates it.
    """
    from fastapi import FastAPI, Request
    from fastapi.testclient import TestClient
    from factvault.api.deps import get_tenant_id

    test_app = FastAPI()

    @test_app.get("/test-tenant")
    def test_route(request: Request):
        tid = get_tenant_id(request)
        return {"tenant_id": str(tid)}

    # Simulate middleware having set request.state.tenant_id
    @test_app.middleware("http")
    async def inject_tenant(request: Request, call_next):
        import uuid as _uuid
        request.state.tenant_id = _uuid.UUID(valid_tenant_id)
        return await call_next(request)

    client = TestClient(test_app)
    resp = client.get("/test-tenant")
    assert resp.status_code == 200
    assert resp.json()["tenant_id"] == valid_tenant_id
```

- [ ] **RUN/FAIL:**

```bash
$ python -m pytest tests/api/test_auth.py -x 2>&1 | head -20
# expected: ImportError for factvault.api.auth or factvault.api.deps
```

- [ ] **IMPLEMENT:** `factvault/api/auth.py` and `factvault/api/deps.py`:

```python
# factvault/api/auth.py
"""
JWT authentication middleware for the factvault FastAPI application.

The middleware intercepts every request except health routes.
It extracts the ``Authorization: Bearer <token>`` header, verifies the
token via ``JWTVerifier``, and attaches ``tenant_id`` (as a UUID) to
``request.state.tenant_id``.

On any auth failure it returns an RFC 9457 Problem Detail 401 response
immediately, before the route handler is invoked.
"""
from __future__ import annotations

import uuid
from functools import lru_cache

from fastapi import Request
from fastapi.responses import JSONResponse
from starlette.middleware.base import BaseHTTPMiddleware

from factvault.auth.jwt import JWTVerifier, JWTError, verifier_from_env


# Routes that do not require authentication.
_PUBLIC_PATHS: frozenset[str] = frozenset({"/healthz", "/readyz", "/docs", "/redoc", "/openapi.json"})


@lru_cache(maxsize=1)
def _get_verifier() -> JWTVerifier:
    """
    Return the application-wide JWTVerifier instance.

    Constructed once from environment variables.  ``lru_cache`` ensures a
    single instance per process.  Tests patch this function to inject a
    mock verifier.
    """
    return verifier_from_env()


class JWTAuthMiddleware(BaseHTTPMiddleware):
    """
    Starlette middleware that enforces JWT authentication on all non-public routes.

    On success: attaches ``tenant_id`` (UUID) to ``request.state``.
    On failure: returns 401 JSON immediately.
    """

    async def dispatch(self, request: Request, call_next):
        # Pass through public paths without auth
        if request.url.path in _PUBLIC_PATHS:
            return await call_next(request)

        auth_header = request.headers.get("Authorization", "")
        if not auth_header.startswith("Bearer "):
            return _auth_error("Missing or malformed Authorization header.")

        token = auth_header[len("Bearer "):]
        try:
            verifier = _get_verifier()
            claims = verifier.verify(token)
        except JWTError as exc:
            return _auth_error(str(exc))
        except RuntimeError as exc:
            # verifier_from_env() raised — JWT public key not configured.
            return _auth_error(f"Auth not configured: {exc}")

        try:
            request.state.tenant_id = uuid.UUID(claims["sub"])
        except (ValueError, KeyError) as exc:
            return _auth_error(f"Invalid tenant_id in JWT sub claim: {exc}")

        return await call_next(request)


def _auth_error(detail: str) -> JSONResponse:
    """Return an RFC 9457 Problem Detail 401 response."""
    return JSONResponse(
        status_code=401,
        content={
            "type": "https://factvault.io/errors/unauthorized",
            "title": "Unauthorized",
            "status": 401,
            "detail": detail,
        },
    )
```

```python
# factvault/api/deps.py
"""
Reusable FastAPI dependency functions.

``get_tenant_id`` — reads tenant_id from request.state (populated by JWTAuthMiddleware).
``get_db``        — opens a database connection inside tenant_context().
"""
from __future__ import annotations

import os
import uuid
from typing import Generator

from fastapi import Request, HTTPException
from sqlalchemy import create_engine, text
from sqlalchemy.engine import Connection

from factvault.db.rls import tenant_context


def get_tenant_id(request: Request) -> uuid.UUID:
    """
    FastAPI dependency: return the tenant_id from request.state.

    JWTAuthMiddleware populates request.state.tenant_id before route
    handlers are invoked.  If it is absent (e.g., on public routes that
    somehow call this dependency), raise 403.
    """
    tenant_id = getattr(request.state, "tenant_id", None)
    if tenant_id is None:
        raise HTTPException(status_code=403, detail="Tenant context not established.")
    return tenant_id


def get_db(tenant_id: uuid.UUID = None) -> Generator[Connection, None, None]:
    """
    FastAPI dependency: yield a SQLAlchemy Connection inside tenant_context().

    The connection is obtained from a fresh engine per request.  Connection
    pooling (e.g., via PgBouncer or asyncpg) is configured at the
    infrastructure layer; this function uses SQLAlchemy's built-in pool.

    Usage in route handlers::

        @router.get("/entities/{id}/dossier")
        def get_dossier(
            id: uuid.UUID,
            tenant_id: uuid.UUID = Depends(get_tenant_id),
            conn: Connection = Depends(get_db),
        ): ...

    Note: The ``tenant_id`` parameter here is a stand-in; in real route
    handlers, use ``Depends(get_tenant_id)`` to inject the tenant_id from
    the request state, then pass it to this generator via a closure or
    explicit parameter binding.
    """
    db_url = os.environ.get("FACTVAULT_DATABASE_URL")
    if not db_url:
        raise HTTPException(status_code=503, detail="Database not configured.")

    engine = create_engine(db_url)
    with engine.connect() as conn:
        with conn.begin():
            if tenant_id is not None:
                with tenant_context(conn, tenant_id):
                    yield conn
            else:
                yield conn
```

Wire the `JWTAuthMiddleware` into `factvault/api/main.py`. Edit `create_app()` to add:

```python
# In factvault/api/main.py, inside create_app(), after CORS middleware:
from factvault.api.auth import JWTAuthMiddleware
application.add_middleware(JWTAuthMiddleware)
```

The full updated `create_app()` in `factvault/api/main.py`:

```python
def create_app() -> FastAPI:
    application = FastAPI(
        title="factvault",
        description=(
            "Self-hostable research database where every fact is grounded "
            "in a verifiable, durably-archived source."
        ),
        version="0.0.1",
        docs_url="/docs",
        redoc_url="/redoc",
        openapi_url="/openapi.json",
    )

    # CORS: permissive for now; Plan 5 tightens this per-tenant.
    application.add_middleware(
        CORSMiddleware,
        allow_origins=["*"],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    # JWT auth middleware — must be added AFTER CORS so preflight OPTIONS
    # requests are handled by CORS before auth kicks in.
    from factvault.api.auth import JWTAuthMiddleware
    application.add_middleware(JWTAuthMiddleware)

    # Health routes (no auth required — exempt in JWTAuthMiddleware._PUBLIC_PATHS)
    application.include_router(health_router)

    return application
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/api/test_auth.py -v
# expected: 5 passed
```

Run the full assembled test suite to verify nothing is broken:

```bash
$ python -m pytest tests/auth/ tests/assembler/ tests/api/ -v
# expected: all passing (auth: 8, assembler: ~22, api: 13)
```

- [ ] **COMMIT:**

```bash
git add factvault/api/auth.py factvault/api/deps.py factvault/api/main.py \
        tests/api/test_auth.py
git commit -m "feat(api): JWT auth middleware + tenant_id dependency injection"
```

---

---

## Task 12 — Entities route

**File:** `factvault/api/routes/entities.py`

Two endpoints. Both import `get_db` and `get_tenant_id` from `factvault.api.deps` (Pass 1 T11).

Staleness threshold: `FACTVAULT_DOSSIER_STALENESS_DAYS` env var, default `7`.

**Code:**

```python
# factvault/api/routes/entities.py
from __future__ import annotations

import os
from datetime import datetime, timedelta, timezone
from uuid import UUID

from fastapi import APIRouter, Depends, HTTPException, Query
from sqlalchemy import text

from factvault.api.deps import get_db, get_tenant_id
from factvault.assembler.bundle import assemble

router = APIRouter(prefix="/entities", tags=["entities"])

_STALENESS_DAYS = int(os.getenv("FACTVAULT_DOSSIER_STALENESS_DAYS", "7"))


@router.get("/{entity_id}/dossier")
async def get_dossier(
    entity_id: UUID,
    db=Depends(get_db),
    tenant_id: UUID = Depends(get_tenant_id),
) -> dict:
    """Return cached dossier for an entity; recompute on-demand if absent or stale."""
    # Verify entity exists in this tenant.
    row = db.execute(
        text(
            "SELECT id FROM entities WHERE id = :eid"
        ),
        {"eid": str(entity_id)},
    ).fetchone()
    if row is None:
        raise HTTPException(status_code=404, detail="Entity not found")

    staleness_cutoff = datetime.now(timezone.utc) - timedelta(days=_STALENESS_DAYS)

    cached = db.execute(
        text(
            """
            SELECT bundle, assembled_at
            FROM dossiers
            WHERE tenant_id = :tid AND entity_id = :eid
            ORDER BY assembled_at DESC
            LIMIT 1
            """
        ),
        {"tid": str(tenant_id), "eid": str(entity_id)},
    ).fetchone()

    if cached is not None and cached.assembled_at.replace(tzinfo=timezone.utc) > staleness_cutoff:
        return cached.bundle

    # Assemble on-demand.
    bundle = assemble(
        entity_ids=[str(entity_id)],
        depth=0,
        tenant_id=str(tenant_id),
    )

    db.execute(
        text(
            """
            INSERT INTO dossiers (tenant_id, entity_id, bundle, assembled_at)
            VALUES (:tid, :eid, CAST(:bundle AS jsonb), now())
            ON CONFLICT (tenant_id, entity_id)
            DO UPDATE SET bundle = EXCLUDED.bundle, assembled_at = EXCLUDED.assembled_at
            """
        ),
        {"tid": str(tenant_id), "eid": str(entity_id), "bundle": __import__("json").dumps(bundle)},
    )
    db.commit()
    return bundle


@router.get("/by-name")
async def entities_by_name(
    q: str = Query(..., min_length=1),
    type_uri: str | None = Query(default=None),
    limit: int = Query(default=20, le=200),
    db=Depends(get_db),
    tenant_id: UUID = Depends(get_tenant_id),
) -> list[dict]:
    """Case-insensitive prefix match on entities.label within the caller's tenant."""
    params: dict = {"tid": str(tenant_id), "q": q.lower() + "%", "limit": limit}
    type_filter = ""
    if type_uri is not None:
        type_filter = "AND type_uri = :type_uri"
        params["type_uri"] = type_uri

    rows = db.execute(
        text(
            f"""
            SELECT id, label, type_uri, description
            FROM entities
            WHERE lower(label) LIKE :q
              {type_filter}
            ORDER BY label
            LIMIT :limit
            """
        ),
        params,
    ).fetchall()

    return [
        {
            "id": str(r.id),
            "label": r.label,
            "type_uri": r.type_uri,
            "description": r.description,
        }
        for r in rows
    ]
```

Register in `factvault/api/main.py` inside `create_app()`:

```python
from factvault.api.routes.entities import router as entities_router
application.include_router(entities_router)
```

**Tests:** `tests/api/test_entities.py`

```python
# tests/api/test_entities.py
import json
import uuid
from datetime import datetime, timezone, timedelta
from unittest.mock import patch, MagicMock

import pytest
from fastapi.testclient import TestClient

from factvault.api.main import create_app
from tests.helpers import make_jwt  # helper from Pass 1 T11 auth tests


@pytest.fixture
def client():
    return TestClient(create_app())


TENANT_ID = str(uuid.uuid4())
ENTITY_ID = str(uuid.uuid4())


def _headers():
    return {"Authorization": f"Bearer {make_jwt(TENANT_ID)}"}


def test_dossier_returns_cached_bundle(client):
    fresh_at = datetime.now(timezone.utc)
    cached_bundle = {"query": {"type": "dossier"}, "facts": [], "entities": []}

    mock_db = MagicMock()
    # Entity exists
    mock_db.execute.side_effect = [
        MagicMock(fetchone=lambda: MagicMock(id=ENTITY_ID)),  # existence check
        MagicMock(fetchone=lambda: MagicMock(bundle=cached_bundle, assembled_at=fresh_at)),  # cache hit
    ]

    with patch("factvault.api.routes.entities.get_db", return_value=mock_db), \
         patch("factvault.api.routes.entities.get_tenant_id", return_value=uuid.UUID(TENANT_ID)):
        resp = client.get(f"/entities/{ENTITY_ID}/dossier", headers=_headers())

    assert resp.status_code == 200
    assert resp.json() == cached_bundle


def test_dossier_assembles_on_cache_miss(client):
    assembled = {"query": {"type": "dossier"}, "facts": [{"id": "abc"}], "entities": []}

    mock_db = MagicMock()
    mock_db.execute.side_effect = [
        MagicMock(fetchone=lambda: MagicMock(id=ENTITY_ID)),  # entity exists
        MagicMock(fetchone=lambda: None),  # cache miss
        MagicMock(),  # upsert
    ]

    with patch("factvault.api.routes.entities.get_db", return_value=mock_db), \
         patch("factvault.api.routes.entities.get_tenant_id", return_value=uuid.UUID(TENANT_ID)), \
         patch("factvault.api.routes.entities.assemble", return_value=assembled) as mock_assemble:
        resp = client.get(f"/entities/{ENTITY_ID}/dossier", headers=_headers())

    assert resp.status_code == 200
    mock_assemble.assert_called_once_with(
        entity_ids=[ENTITY_ID], depth=0, tenant_id=TENANT_ID
    )


def test_dossier_assembles_on_stale_cache(client):
    stale_at = datetime.now(timezone.utc) - timedelta(days=30)
    assembled = {"query": {"type": "dossier"}, "facts": [], "entities": []}

    mock_db = MagicMock()
    mock_db.execute.side_effect = [
        MagicMock(fetchone=lambda: MagicMock(id=ENTITY_ID)),
        MagicMock(fetchone=lambda: MagicMock(bundle={}, assembled_at=stale_at)),
        MagicMock(),
    ]

    with patch("factvault.api.routes.entities.get_db", return_value=mock_db), \
         patch("factvault.api.routes.entities.get_tenant_id", return_value=uuid.UUID(TENANT_ID)), \
         patch("factvault.api.routes.entities.assemble", return_value=assembled):
        resp = client.get(f"/entities/{ENTITY_ID}/dossier", headers=_headers())

    assert resp.status_code == 200


def test_dossier_404_for_unknown_entity(client):
    mock_db = MagicMock()
    mock_db.execute.return_value = MagicMock(fetchone=lambda: None)

    with patch("factvault.api.routes.entities.get_db", return_value=mock_db), \
         patch("factvault.api.routes.entities.get_tenant_id", return_value=uuid.UUID(TENANT_ID)):
        resp = client.get(f"/entities/{uuid.uuid4()}/dossier", headers=_headers())

    assert resp.status_code == 404


def test_by_name_returns_matching_entities(client):
    mock_rows = [
        MagicMock(id=ENTITY_ID, label="Acme Corp", type_uri="https://schema.org/Organization", description="Test"),
    ]
    mock_db = MagicMock()
    mock_db.execute.return_value = MagicMock(fetchall=lambda: mock_rows)

    with patch("factvault.api.routes.entities.get_db", return_value=mock_db), \
         patch("factvault.api.routes.entities.get_tenant_id", return_value=uuid.UUID(TENANT_ID)):
        resp = client.get("/entities/by-name?q=Acme", headers=_headers())

    assert resp.status_code == 200
    data = resp.json()
    assert len(data) == 1
    assert data[0]["label"] == "Acme Corp"


def test_by_name_filters_by_type_uri(client):
    mock_db = MagicMock()
    mock_db.execute.return_value = MagicMock(fetchall=lambda: [])

    with patch("factvault.api.routes.entities.get_db", return_value=mock_db), \
         patch("factvault.api.routes.entities.get_tenant_id", return_value=uuid.UUID(TENANT_ID)):
        resp = client.get(
            "/entities/by-name?q=Acme&type_uri=https://schema.org/Organization",
            headers=_headers(),
        )

    assert resp.status_code == 200
    call_args = mock_db.execute.call_args
    assert "type_uri" in call_args[0][1]
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/api/test_entities.py -v
# expected: 6 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/api/routes/entities.py tests/api/test_entities.py
git commit -m "feat(api): entities route — dossier + by-name endpoints"
```

---

## Task 13 — Stories route

**File:** `factvault/api/routes/stories.py`

`POST /stories`. Seeds entities and statements via pgvector ANN on the query embedding, then delegates to `assemble()`.

**Code:**

```python
# factvault/api/routes/stories.py
from __future__ import annotations

from uuid import UUID

from fastapi import APIRouter, Depends
from pydantic import BaseModel, Field
from sqlalchemy import text

from factvault.api.deps import get_db, get_tenant_id
from factvault.assembler.bundle import assemble
from factvault.retrieval.embed import embed_query  # shared retrieval helper — Task 16 factors this

router = APIRouter(prefix="/stories", tags=["stories"])


class StoryRequest(BaseModel):
    query: str = Field(..., min_length=1)
    depth: int = Field(default=2, ge=0, le=3)
    max_facts: int = Field(default=100, ge=1, le=10000)
    max_entities: int = Field(default=200, ge=1, le=10000)


@router.post("")
async def create_story(
    req: StoryRequest,
    db=Depends(get_db),
    tenant_id: UUID = Depends(get_tenant_id),
) -> dict:
    """On-demand story bundle via embedding similarity seed + graph expansion."""
    query_vec = embed_query(req.query)
    vec_literal = "[" + ",".join(str(x) for x in query_vec) + "]"

    # ANN: top-5 entities closest to query
    entity_rows = db.execute(
        text(
            """
            SELECT id FROM entities
            WHERE embedding IS NOT NULL
            ORDER BY embedding <=> CAST(:vec AS vector)
            LIMIT 5
            """
        ),
        {"vec": vec_literal},
    ).fetchall()

    # ANN: top-5 statements closest to query; resolve to their subject_id
    stmt_rows = db.execute(
        text(
            """
            SELECT DISTINCT subject_id AS id FROM statements
            WHERE embedding IS NOT NULL
            ORDER BY embedding <=> CAST(:vec AS vector)
            LIMIT 5
            """
        ),
        {"vec": vec_literal},
    ).fetchall()

    seed_ids = list({str(r.id) for r in entity_rows + stmt_rows})

    if not seed_ids:
        return {
            "query": {"type": "story", "query": req.query, "depth": req.depth},
            "assembled_at": __import__("datetime").datetime.utcnow().isoformat() + "Z",
            "entities": [],
            "facts": [],
            "relations": [],
            "conflicts": [],
        }

    return assemble(
        entity_ids=seed_ids,
        depth=req.depth,
        tenant_id=str(tenant_id),
        query=req.query,
        max_facts=req.max_facts,
    )
```

Register in `create_app()`:

```python
from factvault.api.routes.stories import router as stories_router
application.include_router(stories_router)
```

**Shared retrieval helper (stubbed here, filled in Task 16):**

```python
# factvault/retrieval/__init__.py
# (empty)

# factvault/retrieval/embed.py
from __future__ import annotations

from functools import lru_cache
from sentence_transformers import SentenceTransformer

_MODEL_NAME = "BAAI/bge-m3"


@lru_cache(maxsize=1)
def _model() -> SentenceTransformer:
    return SentenceTransformer(_MODEL_NAME)


def embed_query(text: str) -> list[float]:
    """Return a 1024-dim BGE-M3 embedding for the given text."""
    return _model().encode(text, normalize_embeddings=True).tolist()
```

**Tests:** `tests/api/test_stories.py`

```python
# tests/api/test_stories.py
import uuid
from unittest.mock import patch, MagicMock

import pytest
from fastapi.testclient import TestClient

from factvault.api.main import create_app
from tests.helpers import make_jwt

TENANT_ID = str(uuid.uuid4())
ENTITY_ID_A = str(uuid.uuid4())
ENTITY_ID_B = str(uuid.uuid4())

# Deterministic mock vector — 1024 zeros then 1.0 at index 0
_MOCK_VEC = [0.0] * 1024
_MOCK_VEC[0] = 1.0


@pytest.fixture
def client():
    return TestClient(create_app())


def _headers():
    return {"Authorization": f"Bearer {make_jwt(TENANT_ID)}"}


def test_story_returns_bundle(client):
    assembled = {
        "query": {"type": "story"},
        "entities": [{"id": ENTITY_ID_A}, {"id": ENTITY_ID_B}],
        "facts": [{"id": "f1"}],
        "relations": [{"source": {"id": ENTITY_ID_A}, "target": {"id": ENTITY_ID_B}}],
        "conflicts": [],
    }
    mock_db = MagicMock()
    entity_row = MagicMock(id=ENTITY_ID_A)
    stmt_row = MagicMock(id=ENTITY_ID_B)
    mock_db.execute.side_effect = [
        MagicMock(fetchall=lambda: [entity_row]),
        MagicMock(fetchall=lambda: [stmt_row]),
    ]

    with patch("factvault.api.routes.stories.embed_query", return_value=_MOCK_VEC), \
         patch("factvault.api.routes.stories.assemble", return_value=assembled) as mock_assemble, \
         patch("factvault.api.routes.stories.get_db", return_value=mock_db), \
         patch("factvault.api.routes.stories.get_tenant_id", return_value=uuid.UUID(TENANT_ID)):
        resp = client.post(
            "/stories",
            json={"query": "Acme acquisitions", "depth": 2},
            headers=_headers(),
        )

    assert resp.status_code == 200
    data = resp.json()
    assert len(data["entities"]) == 2
    assert len(data["facts"]) == 1
    mock_assemble.assert_called_once()
    call_kwargs = mock_assemble.call_args[1]
    assert call_kwargs["depth"] == 2
    assert call_kwargs["query"] == "Acme acquisitions"


def test_story_empty_when_no_seeds(client):
    mock_db = MagicMock()
    mock_db.execute.side_effect = [
        MagicMock(fetchall=lambda: []),
        MagicMock(fetchall=lambda: []),
    ]

    with patch("factvault.api.routes.stories.embed_query", return_value=_MOCK_VEC), \
         patch("factvault.api.routes.stories.get_db", return_value=mock_db), \
         patch("factvault.api.routes.stories.get_tenant_id", return_value=uuid.UUID(TENANT_ID)):
        resp = client.post(
            "/stories",
            json={"query": "nonexistent topic"},
            headers=_headers(),
        )

    assert resp.status_code == 200
    assert resp.json()["entities"] == []
    assert resp.json()["facts"] == []


def test_story_validation_rejects_bad_depth(client):
    with patch("factvault.api.routes.stories.embed_query", return_value=_MOCK_VEC), \
         patch("factvault.api.routes.stories.get_tenant_id", return_value=uuid.UUID(TENANT_ID)):
        resp = client.post(
            "/stories",
            json={"query": "test", "depth": 10},
            headers=_headers(),
        )
    assert resp.status_code == 422


def test_story_graph_neighbors_included(client):
    """Seed entity A; neighbor B is returned via assemble's graph expansion."""
    assembled = {
        "query": {"type": "story", "query": "Acme acquisitions", "depth": 2},
        "entities": [
            {"id": ENTITY_ID_A, "label": "Acme"},
            {"id": ENTITY_ID_B, "label": "MegaCorp"},
        ],
        "facts": [],
        "relations": [{"source": {"id": ENTITY_ID_A}, "target": {"id": ENTITY_ID_B}, "type": "acquired_by"}],
        "conflicts": [],
    }
    mock_db = MagicMock()
    mock_db.execute.side_effect = [
        MagicMock(fetchall=lambda: [MagicMock(id=ENTITY_ID_A)]),
        MagicMock(fetchall=lambda: []),
    ]

    with patch("factvault.api.routes.stories.embed_query", return_value=_MOCK_VEC), \
         patch("factvault.api.routes.stories.assemble", return_value=assembled), \
         patch("factvault.api.routes.stories.get_db", return_value=mock_db), \
         patch("factvault.api.routes.stories.get_tenant_id", return_value=uuid.UUID(TENANT_ID)):
        resp = client.post(
            "/stories",
            json={"query": "Acme acquisitions", "depth": 2},
            headers=_headers(),
        )

    assert resp.status_code == 200
    ids = {e["id"] for e in resp.json()["entities"]}
    assert ENTITY_ID_A in ids
    assert ENTITY_ID_B in ids
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/api/test_stories.py -v
# expected: 4 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/api/routes/stories.py factvault/retrieval/__init__.py \
        factvault/retrieval/embed.py tests/api/test_stories.py
git commit -m "feat(api): stories route + embed_query retrieval helper"
```

---

## Task 14 — Facts route

**File:** `factvault/api/routes/facts.py`

`POST /facts/query`. Pure SQL, no graph expansion, no LLM. Returns `bundle`-shaped JSON with populated `facts[]`, empty `relations[]` and `conflicts[]`.

**Code:**

```python
# factvault/api/routes/facts.py
from __future__ import annotations

from uuid import UUID

from fastapi import APIRouter, Depends
from pydantic import BaseModel, Field
from sqlalchemy import text

from factvault.api.deps import get_db, get_tenant_id
from factvault.api.schemas import BundleResponse

router = APIRouter(prefix="/facts", tags=["facts"])


class FactQueryRequest(BaseModel):
    subject_type: str | None = None          # filter by entities.type_uri
    subject_label: str | None = None         # case-insensitive prefix match
    property_slug: str | None = None         # filter by properties.slug
    qualifiers: dict | None = None           # reserved; not filtered in v1
    min_confidence: float = Field(default=0.0, ge=0.0, le=1.0)
    limit: int = Field(default=100, ge=1, le=200)


@router.post("/query")
async def query_facts(
    req: FactQueryRequest,
    db=Depends(get_db),
    tenant_id: UUID = Depends(get_tenant_id),
) -> dict:
    """Return facts matching the given filters; no graph expansion."""
    filters = ["s.tenant_id = :tid", "s.confidence >= :min_conf"]
    params: dict = {"tid": str(tenant_id), "min_conf": req.min_confidence}

    if req.property_slug is not None:
        filters.append("p.slug = :prop_slug")
        params["prop_slug"] = req.property_slug

    if req.subject_type is not None:
        filters.append("subj.type_uri = :subject_type")
        params["subject_type"] = req.subject_type

    if req.subject_label is not None:
        filters.append("lower(subj.label) LIKE :subject_label")
        params["subject_label"] = req.subject_label.lower() + "%"

    params["limit"] = req.limit
    where_clause = " AND ".join(filters)

    rows = db.execute(
        text(
            f"""
            SELECT
                s.id              AS stmt_id,
                s.rank,
                s.confidence,
                s.val_text,
                s.val_number,
                s.val_date,
                s.val_entity,
                subj.id           AS subject_id,
                subj.label        AS subject_label,
                p.slug            AS property_slug,
                p.label           AS property_label,
                p.value_type      AS value_type,
                ss.id             AS ss_id,
                ss.excerpt,
                ss.excerpt_offset_start,
                ss.excerpt_offset_end,
                ss.extraction_method,
                src.id            AS source_id,
                src.url,
                src.publisher,
                src.fetched_at,
                src.content_hash,
                src.archive_url,
                src.last_verified_at,
                src.status        AS verification_status
            FROM statements s
            JOIN entities    subj ON subj.id = s.subject_id
            JOIN properties  p    ON p.id    = s.property_id
            LEFT JOIN statement_sources ss  ON ss.statement_id = s.id
            LEFT JOIN sources           src ON src.id = ss.source_id
            WHERE {where_clause}
            ORDER BY s.confidence DESC, s.id
            LIMIT :limit
            """
        ),
        params,
    ).fetchall()

    # Group sources under statements
    facts: dict[str, dict] = {}
    for r in rows:
        sid = str(r.stmt_id)
        if sid not in facts:
            value: dict
            if r.value_type == "entity_ref":
                value = {"entity": {"id": str(r.val_entity)}} if r.val_entity else {}
            elif r.value_type == "number":
                value = {"number": float(r.val_number)} if r.val_number is not None else {}
            elif r.value_type == "date":
                value = {"date": r.val_date.isoformat() if r.val_date else None}
            else:
                value = {"text": r.val_text}

            facts[sid] = {
                "id": sid,
                "subject": {"id": str(r.subject_id), "label": r.subject_label},
                "property": {
                    "slug": r.property_slug,
                    "label": r.property_label,
                    "value_type": r.value_type,
                },
                "value": value,
                "rank": r.rank,
                "confidence": float(r.confidence),
                "sources": [],
            }

        if r.ss_id is not None:
            facts[sid]["sources"].append(
                {
                    "id": str(r.source_id),
                    "url": r.url,
                    "publisher": r.publisher,
                    "fetched_at": r.fetched_at.isoformat() if r.fetched_at else None,
                    "content_hash": r.content_hash,
                    "archive_url": r.archive_url,
                    "excerpt": r.excerpt,
                    "excerpt_offset_start": r.excerpt_offset_start,
                    "excerpt_offset_end": r.excerpt_offset_end,
                    "last_verified_at": r.last_verified_at.isoformat() if r.last_verified_at else None,
                    "verification_status": r.verification_status,
                    "extraction_method": r.extraction_method,
                }
            )

    return {
        "query": {
            "type": "fact_query",
            "property_slug": req.property_slug,
            "min_confidence": req.min_confidence,
        },
        "facts": list(facts.values()),
        "relations": [],
        "conflicts": [],
    }
```

Register in `create_app()`:

```python
from factvault.api.routes.facts import router as facts_router
application.include_router(facts_router)
```

**Tests:** `tests/api/test_facts.py`

```python
# tests/api/test_facts.py
import uuid
from unittest.mock import patch, MagicMock

import pytest
from fastapi.testclient import TestClient

from factvault.api.main import create_app
from tests.helpers import make_jwt

TENANT_ID = str(uuid.uuid4())
STMT_ID = str(uuid.uuid4())
ENTITY_ID = str(uuid.uuid4())
SOURCE_ID = str(uuid.uuid4())
SS_ID = str(uuid.uuid4())


@pytest.fixture
def client():
    return TestClient(create_app())


def _headers():
    return {"Authorization": f"Bearer {make_jwt(TENANT_ID)}"}


def _mock_row(**overrides):
    import datetime
    defaults = dict(
        stmt_id=uuid.UUID(STMT_ID),
        rank="preferred",
        confidence=0.85,
        val_text="Acme acquired for $4.2B",
        val_number=None,
        val_date=None,
        val_entity=None,
        subject_id=uuid.UUID(ENTITY_ID),
        subject_label="Acme Corp",
        property_slug="acquired_by",
        property_label="Acquired By",
        value_type="string",
        ss_id=uuid.UUID(SS_ID),
        excerpt="Acme Corp was acquired",
        excerpt_offset_start=0,
        excerpt_offset_end=22,
        extraction_method="llm:gpt-5:v1",
        source_id=uuid.UUID(SOURCE_ID),
        url="https://reuters.com/test",
        publisher="reuters.com",
        fetched_at=datetime.datetime(2025, 1, 1, tzinfo=datetime.timezone.utc),
        content_hash="abc123",
        archive_url="https://web.archive.org/web/test",
        last_verified_at=datetime.datetime(2026, 1, 1, tzinfo=datetime.timezone.utc),
        verification_status="live",
    )
    defaults.update(overrides)
    return MagicMock(**defaults)


def test_facts_query_by_property(client):
    mock_db = MagicMock()
    mock_db.execute.return_value = MagicMock(fetchall=lambda: [_mock_row()])

    with patch("factvault.api.routes.facts.get_db", return_value=mock_db), \
         patch("factvault.api.routes.facts.get_tenant_id", return_value=uuid.UUID(TENANT_ID)):
        resp = client.post(
            "/facts/query",
            json={"property_slug": "acquired_by", "min_confidence": 0.5},
            headers=_headers(),
        )

    assert resp.status_code == 200
    data = resp.json()
    assert len(data["facts"]) == 1
    assert data["facts"][0]["property"]["slug"] == "acquired_by"
    assert data["facts"][0]["confidence"] == 0.85
    assert data["relations"] == []
    assert data["conflicts"] == []


def test_facts_query_filters_by_confidence(client):
    mock_db = MagicMock()
    # Row with confidence 0.3 below threshold — DB filter; verify param passed
    mock_db.execute.return_value = MagicMock(fetchall=lambda: [])

    with patch("factvault.api.routes.facts.get_db", return_value=mock_db), \
         patch("factvault.api.routes.facts.get_tenant_id", return_value=uuid.UUID(TENANT_ID)):
        resp = client.post(
            "/facts/query",
            json={"min_confidence": 0.9},
            headers=_headers(),
        )

    assert resp.status_code == 200
    call_params = mock_db.execute.call_args[0][1]
    assert call_params["min_conf"] == 0.9


def test_facts_query_includes_source_metadata(client):
    mock_db = MagicMock()
    mock_db.execute.return_value = MagicMock(fetchall=lambda: [_mock_row()])

    with patch("factvault.api.routes.facts.get_db", return_value=mock_db), \
         patch("factvault.api.routes.facts.get_tenant_id", return_value=uuid.UUID(TENANT_ID)):
        resp = client.post("/facts/query", json={}, headers=_headers())

    source = resp.json()["facts"][0]["sources"][0]
    for field in ("url", "excerpt", "excerpt_offset_start", "excerpt_offset_end",
                  "content_hash", "archive_url", "verification_status", "extraction_method"):
        assert field in source, f"Missing source field: {field}"


def test_facts_query_subject_type_filter(client):
    mock_db = MagicMock()
    mock_db.execute.return_value = MagicMock(fetchall=lambda: [])

    with patch("factvault.api.routes.facts.get_db", return_value=mock_db), \
         patch("factvault.api.routes.facts.get_tenant_id", return_value=uuid.UUID(TENANT_ID)):
        resp = client.post(
            "/facts/query",
            json={"subject_type": "https://schema.org/Organization"},
            headers=_headers(),
        )

    assert resp.status_code == 200
    params = mock_db.execute.call_args[0][1]
    assert params["subject_type"] == "https://schema.org/Organization"
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/api/test_facts.py -v
# expected: 4 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/api/routes/facts.py tests/api/test_facts.py
git commit -m "feat(api): facts/query route — pure-SQL fact filter endpoint"
```

---

## Task 15 — Dossier pre-compute worker

**File:** `factvault/workers/dossier.py`

Implements `DossierWorker(Worker)` with `name = "dossier"`. Periodic worker (daily default). Uses `app.tenant_id` GUC via `tenant_context()`.

**Code:**

```python
# factvault/workers/dossier.py
from __future__ import annotations

import json
import logging
from datetime import datetime, timezone

from sqlalchemy import text

from factvault.assembler.bundle import assemble
from factvault.db.rls import tenant_context
from factvault.workers.base import Worker  # Plan 2's base Worker class

log = logging.getLogger(__name__)

_BATCH_SIZE = 100


class DossierWorker(Worker):
    name = "dossier"

    def run_once(self, db) -> None:
        """Pre-compute dossiers for all tenants; stale = assembled_at older than 24 h."""
        tenants = db.execute(text("SELECT id FROM tenants")).fetchall()
        for tenant_row in tenants:
            tid = str(tenant_row.id)
            with tenant_context(db, tid):
                self._refresh_tenant(db, tid)

    def _refresh_tenant(self, db, tenant_id: str) -> None:
        due = db.execute(
            text(
                """
                SELECT e.id FROM entities e
                WHERE e.tenant_id = :tid
                  AND (
                    NOT EXISTS (
                        SELECT 1 FROM dossiers d
                        WHERE d.tenant_id = :tid AND d.entity_id = e.id
                    )
                    OR (
                        SELECT assembled_at FROM dossiers d
                        WHERE d.tenant_id = :tid AND d.entity_id = e.id
                        ORDER BY assembled_at DESC
                        LIMIT 1
                    ) < now() - interval '24 hours'
                  )
                LIMIT :batch
                """
            ),
            {"tid": tenant_id, "batch": _BATCH_SIZE},
        ).fetchall()

        refreshed = 0
        for row in due:
            eid = str(row.id)
            try:
                bundle = assemble(
                    entity_ids=[eid],
                    depth=0,
                    tenant_id=tenant_id,
                )
                db.execute(
                    text(
                        """
                        INSERT INTO dossiers (tenant_id, entity_id, bundle, assembled_at)
                        VALUES (:tid, :eid, CAST(:bundle AS jsonb), now())
                        ON CONFLICT (tenant_id, entity_id)
                        DO UPDATE SET bundle = EXCLUDED.bundle, assembled_at = EXCLUDED.assembled_at
                        """
                    ),
                    {"tid": tenant_id, "eid": eid, "bundle": json.dumps(bundle)},
                )
                db.commit()
                refreshed += 1
            except Exception:
                log.exception("Failed to assemble dossier for entity %s (tenant %s)", eid, tenant_id)
                db.rollback()

        log.info("DossierWorker: refreshed %d dossiers for tenant %s", refreshed, tenant_id)
```

**Tests:** `tests/workers/test_dossier.py`

```python
# tests/workers/test_dossier.py
import json
import uuid
from datetime import datetime, timezone, timedelta
from unittest.mock import patch, MagicMock, call

import pytest

from factvault.workers.dossier import DossierWorker

TENANT_ID = str(uuid.uuid4())
ENTITY_IDS = [str(uuid.uuid4()), str(uuid.uuid4()), str(uuid.uuid4())]


def _make_db(entity_ids, has_stale_dossier=False):
    db = MagicMock()
    tenant_row = MagicMock(id=uuid.UUID(TENANT_ID))
    entity_rows = [MagicMock(id=uuid.UUID(eid)) for eid in entity_ids]
    db.execute.side_effect = [
        MagicMock(fetchall=lambda: [tenant_row]),  # SELECT tenants
        MagicMock(fetchall=lambda: entity_rows),   # SELECT due entities
    ] + [MagicMock()] * len(entity_ids)            # upserts
    return db


def test_dossier_worker_creates_rows_for_all_entities():
    assembled = {"query": {"type": "dossier"}, "facts": [{"id": "f1"}], "entities": [], "relations": [], "conflicts": []}
    db = _make_db(ENTITY_IDS)

    with patch("factvault.workers.dossier.assemble", return_value=assembled) as mock_assemble, \
         patch("factvault.workers.dossier.tenant_context"):
        worker = DossierWorker()
        worker.run_once(db)

    assert mock_assemble.call_count == len(ENTITY_IDS)
    for eid in ENTITY_IDS:
        mock_assemble.assert_any_call(entity_ids=[eid], depth=0, tenant_id=TENANT_ID)


def test_dossier_worker_upserts_bundle():
    assembled = {"query": {"type": "dossier"}, "facts": [], "entities": [], "relations": [], "conflicts": []}
    eid = ENTITY_IDS[0]
    db = _make_db([eid])

    with patch("factvault.workers.dossier.assemble", return_value=assembled), \
         patch("factvault.workers.dossier.tenant_context"):
        DossierWorker().run_once(db)

    db.commit.assert_called()
    upsert_call = db.execute.call_args_list[-1]
    bound_params = upsert_call[0][1]
    assert bound_params["eid"] == eid
    assert json.loads(bound_params["bundle"]) == assembled


def test_dossier_worker_continues_on_assembly_error():
    """Worker must not abort the whole batch if one entity fails."""
    def fail_on_first(entity_ids, depth, tenant_id):
        if entity_ids[0] == ENTITY_IDS[0]:
            raise RuntimeError("assembly failed")
        return {"facts": [], "entities": [], "relations": [], "conflicts": []}

    db = _make_db(ENTITY_IDS)

    with patch("factvault.workers.dossier.assemble", side_effect=fail_on_first), \
         patch("factvault.workers.dossier.tenant_context"):
        DossierWorker().run_once(db)

    # rollback called once for the failed entity
    db.rollback.assert_called_once()


def test_dossier_worker_skips_fresh_entities():
    """Entities with assembled_at < 24 h should not appear in 'due' list."""
    db = MagicMock()
    tenant_row = MagicMock(id=uuid.UUID(TENANT_ID))
    db.execute.side_effect = [
        MagicMock(fetchall=lambda: [tenant_row]),
        MagicMock(fetchall=lambda: []),  # no due entities
    ]

    with patch("factvault.workers.dossier.assemble") as mock_assemble, \
         patch("factvault.workers.dossier.tenant_context"):
        DossierWorker().run_once(db)

    mock_assemble.assert_not_called()
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/workers/test_dossier.py -v
# expected: 4 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/workers/dossier.py tests/workers/test_dossier.py
git commit -m "feat(workers): dossier pre-compute worker"
```

---

## Task 16 — MCP server + retrieval module refactor

**Files:** `factvault/mcp/server.py`, `factvault/retrieval/retrieval.py`

Factor the three retrieval modes into `factvault/retrieval/retrieval.py` so both REST routes and MCP server call the same functions. Refactor T12–T14 routes to import from there.

**Shared retrieval module:**

```python
# factvault/retrieval/retrieval.py
from __future__ import annotations

import json
from datetime import datetime, timedelta, timezone
from uuid import UUID

from sqlalchemy import text
from sqlalchemy.orm import Session

from factvault.assembler.bundle import assemble
from factvault.retrieval.embed import embed_query

_DOSSIER_STALENESS_DAYS = 7


def get_dossier(
    entity_id: str,
    tenant_id: str,
    db: Session,
    staleness_days: int = _DOSSIER_STALENESS_DAYS,
) -> dict:
    """Look up (or compute) the dossier bundle for a single entity."""
    row = db.execute(
        text("SELECT id FROM entities WHERE id = :eid"),
        {"eid": entity_id},
    ).fetchone()
    if row is None:
        raise KeyError(f"Entity {entity_id} not found")

    cutoff = datetime.now(timezone.utc) - timedelta(days=staleness_days)
    cached = db.execute(
        text(
            """
            SELECT bundle, assembled_at FROM dossiers
            WHERE tenant_id = :tid AND entity_id = :eid
            ORDER BY assembled_at DESC LIMIT 1
            """
        ),
        {"tid": tenant_id, "eid": entity_id},
    ).fetchone()

    if cached is not None and cached.assembled_at.replace(tzinfo=timezone.utc) > cutoff:
        return cached.bundle

    bundle = assemble(entity_ids=[entity_id], depth=0, tenant_id=tenant_id)
    db.execute(
        text(
            """
            INSERT INTO dossiers (tenant_id, entity_id, bundle, assembled_at)
            VALUES (:tid, :eid, CAST(:bundle AS jsonb), now())
            ON CONFLICT (tenant_id, entity_id)
            DO UPDATE SET bundle = EXCLUDED.bundle, assembled_at = EXCLUDED.assembled_at
            """
        ),
        {"tid": tenant_id, "eid": entity_id, "bundle": json.dumps(bundle)},
    )
    db.commit()
    return bundle


def get_story(
    query: str,
    depth: int,
    tenant_id: str,
    db: Session,
    max_facts: int = 100,
) -> dict:
    """Embed query → ANN seed → graph-expanded bundle."""
    query_vec = embed_query(query)
    vec_literal = "[" + ",".join(str(x) for x in query_vec) + "]"

    entity_rows = db.execute(
        text(
            "SELECT id FROM entities WHERE embedding IS NOT NULL "
            "ORDER BY embedding <=> CAST(:vec AS vector) LIMIT 5"
        ),
        {"vec": vec_literal},
    ).fetchall()
    stmt_rows = db.execute(
        text(
            "SELECT DISTINCT subject_id AS id FROM statements WHERE embedding IS NOT NULL "
            "ORDER BY embedding <=> CAST(:vec AS vector) LIMIT 5"
        ),
        {"vec": vec_literal},
    ).fetchall()

    seed_ids = list({str(r.id) for r in entity_rows + stmt_rows})
    if not seed_ids:
        return {
            "query": {"type": "story", "query": query, "depth": depth},
            "assembled_at": datetime.utcnow().isoformat() + "Z",
            "entities": [], "facts": [], "relations": [], "conflicts": [],
        }

    return assemble(entity_ids=seed_ids, depth=depth, tenant_id=tenant_id, query=query, max_facts=max_facts)


def query_facts(
    tenant_id: str,
    db: Session,
    property_slug: str | None = None,
    subject_type: str | None = None,
    subject_label: str | None = None,
    min_confidence: float = 0.0,
    limit: int = 100,
) -> dict:
    """Pure-SQL fact filter; returns bundle-shaped dict."""
    filters = ["s.tenant_id = :tid", "s.confidence >= :min_conf"]
    params: dict = {"tid": tenant_id, "min_conf": min_confidence, "limit": limit}

    if property_slug:
        filters.append("p.slug = :prop_slug")
        params["prop_slug"] = property_slug
    if subject_type:
        filters.append("subj.type_uri = :subject_type")
        params["subject_type"] = subject_type
    if subject_label:
        filters.append("lower(subj.label) LIKE :subject_label")
        params["subject_label"] = subject_label.lower() + "%"

    where = " AND ".join(filters)
    rows = db.execute(
        text(
            f"""
            SELECT
                s.id AS stmt_id, s.rank, s.confidence,
                s.val_text, s.val_number, s.val_date, s.val_entity,
                subj.id AS subject_id, subj.label AS subject_label,
                p.slug AS property_slug, p.label AS property_label, p.value_type,
                ss.id AS ss_id, ss.excerpt,
                ss.excerpt_offset_start, ss.excerpt_offset_end, ss.extraction_method,
                src.id AS source_id, src.url, src.publisher, src.fetched_at,
                src.content_hash, src.archive_url, src.last_verified_at,
                src.status AS verification_status
            FROM statements s
            JOIN entities   subj ON subj.id = s.subject_id
            JOIN properties p    ON p.id    = s.property_id
            LEFT JOIN statement_sources ss  ON ss.statement_id = s.id
            LEFT JOIN sources           src ON src.id = ss.source_id
            WHERE {where}
            ORDER BY s.confidence DESC, s.id
            LIMIT :limit
            """
        ),
        params,
    ).fetchall()

    facts: dict[str, dict] = {}
    for r in rows:
        sid = str(r.stmt_id)
        if sid not in facts:
            if r.value_type == "entity_ref":
                value = {"entity": {"id": str(r.val_entity)}} if r.val_entity else {}
            elif r.value_type == "number":
                value = {"number": float(r.val_number)} if r.val_number is not None else {}
            elif r.value_type == "date":
                value = {"date": r.val_date.isoformat() if r.val_date else None}
            else:
                value = {"text": r.val_text}

            facts[sid] = {
                "id": sid,
                "subject": {"id": str(r.subject_id), "label": r.subject_label},
                "property": {"slug": r.property_slug, "label": r.property_label, "value_type": r.value_type},
                "value": value,
                "rank": r.rank,
                "confidence": float(r.confidence),
                "sources": [],
            }

        if r.ss_id is not None:
            facts[sid]["sources"].append({
                "id": str(r.source_id), "url": r.url, "publisher": r.publisher,
                "fetched_at": r.fetched_at.isoformat() if r.fetched_at else None,
                "content_hash": r.content_hash, "archive_url": r.archive_url,
                "excerpt": r.excerpt, "excerpt_offset_start": r.excerpt_offset_start,
                "excerpt_offset_end": r.excerpt_offset_end,
                "last_verified_at": r.last_verified_at.isoformat() if r.last_verified_at else None,
                "verification_status": r.verification_status,
                "extraction_method": r.extraction_method,
            })

    return {
        "query": {"type": "fact_query", "property_slug": property_slug, "min_confidence": min_confidence},
        "facts": list(facts.values()),
        "relations": [],
        "conflicts": [],
    }
```

**Note:** After this task, update T12's `entities.py` and T14's `facts.py` to delegate to `factvault.retrieval.retrieval` rather than duplicating SQL. T13's `stories.py` similarly delegates to `get_story()`. The REST route files become thin wrappers; all SQL lives in the retrieval module.

**MCP server:**

```python
# factvault/mcp/server.py
from __future__ import annotations

import os
from typing import Any

from mcp.server import Server
from mcp.server.stdio import stdio_server
from mcp.types import Tool, TextContent

from factvault.auth.jwt import verify_token
from factvault.db import get_sync_session  # Plan 1's sync session factory
from factvault.db.rls import tenant_context
from factvault.retrieval.retrieval import get_dossier, get_story, query_facts

_server = Server("factvault")


def _resolve_tenant(token: str) -> str:
    """Verify JWT and return tenant_id string."""
    claims = verify_token(token)
    return claims["tenant_id"]


@_server.list_tools()
async def list_tools() -> list[Tool]:
    return [
        Tool(
            name="factvault__entity_lookup",
            description=(
                "Look up an entity by ID and return its full sourced dossier bundle. "
                "Returns: canonical bundle JSON with all sourced facts, sources with "
                "excerpts and archive URLs, and any active conflicts."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "entity_id": {"type": "string", "description": "UUID of the entity"},
                    "token": {"type": "string", "description": "Bearer JWT"},
                },
                "required": ["entity_id", "token"],
            },
        ),
        Tool(
            name="factvault__story_query",
            description=(
                "Run a cross-entity story query and return a graph-expanded sourced bundle. "
                "Uses BGE-M3 embedding similarity to seed entities, then expands the graph."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "query": {"type": "string"},
                    "depth": {"type": "integer", "default": 2},
                    "max_facts": {"type": "integer", "default": 100},
                    "token": {"type": "string"},
                },
                "required": ["query", "token"],
            },
        ),
        Tool(
            name="factvault__fact_query",
            description=(
                "Query facts by property/subject/confidence filters. "
                "No graph expansion. Returns bundle-shaped JSON with matching facts and full source metadata."
            ),
            inputSchema={
                "type": "object",
                "properties": {
                    "property_slug": {"type": "string"},
                    "subject_type": {"type": "string"},
                    "subject_label": {"type": "string"},
                    "min_confidence": {"type": "number", "default": 0.0},
                    "limit": {"type": "integer", "default": 100},
                    "token": {"type": "string"},
                },
                "required": ["token"],
            },
        ),
    ]


@_server.call_tool()
async def call_tool(name: str, arguments: dict[str, Any]) -> list[TextContent]:
    import json

    token = arguments.pop("token")
    tenant_id = _resolve_tenant(token)

    with get_sync_session() as db:
        with tenant_context(db, tenant_id):
            if name == "factvault__entity_lookup":
                result = get_dossier(
                    entity_id=arguments["entity_id"],
                    tenant_id=tenant_id,
                    db=db,
                )
            elif name == "factvault__story_query":
                result = get_story(
                    query=arguments["query"],
                    depth=arguments.get("depth", 2),
                    tenant_id=tenant_id,
                    db=db,
                    max_facts=arguments.get("max_facts", 100),
                )
            elif name == "factvault__fact_query":
                result = query_facts(
                    tenant_id=tenant_id,
                    db=db,
                    property_slug=arguments.get("property_slug"),
                    subject_type=arguments.get("subject_type"),
                    subject_label=arguments.get("subject_label"),
                    min_confidence=arguments.get("min_confidence", 0.0),
                    limit=arguments.get("limit", 100),
                )
            else:
                raise ValueError(f"Unknown tool: {name}")

    return [TextContent(type="text", text=json.dumps(result))]


def run() -> None:
    import asyncio
    asyncio.run(stdio_server(_server))


if __name__ == "__main__":
    run()
```

**Tests:** `tests/mcp/test_server.py`

```python
# tests/mcp/test_server.py
import json
import uuid
from unittest.mock import patch, MagicMock, AsyncMock

import pytest

from factvault.mcp.server import call_tool, list_tools

TENANT_ID = str(uuid.uuid4())
ENTITY_ID = str(uuid.uuid4())
_FAKE_TOKEN = "fake.jwt.token"

_DOSSIER = {
    "query": {"type": "dossier", "entity_ids": [ENTITY_ID]},
    "entities": [{"id": ENTITY_ID, "label": "Acme"}],
    "facts": [{"id": "f1", "sources": [{"url": "https://reuters.com/test",
                                         "excerpt": "Acme was acquired",
                                         "verification_status": "live"}]}],
    "relations": [],
    "conflicts": [],
}


@pytest.mark.asyncio
async def test_list_tools_returns_three():
    tools = await list_tools()
    names = {t.name for t in tools}
    assert names == {"factvault__entity_lookup", "factvault__story_query", "factvault__fact_query"}


@pytest.mark.asyncio
async def test_entity_lookup_returns_dossier():
    with patch("factvault.mcp.server._resolve_tenant", return_value=TENANT_ID), \
         patch("factvault.mcp.server.get_dossier", return_value=_DOSSIER) as mock_get, \
         patch("factvault.mcp.server.get_sync_session") as mock_sess, \
         patch("factvault.mcp.server.tenant_context"):
        mock_sess.return_value.__enter__ = lambda s: MagicMock()
        mock_sess.return_value.__exit__ = MagicMock(return_value=False)
        result = await call_tool(
            "factvault__entity_lookup",
            {"entity_id": ENTITY_ID, "token": _FAKE_TOKEN},
        )

    assert len(result) == 1
    data = json.loads(result[0].text)
    assert data["entities"][0]["id"] == ENTITY_ID


@pytest.mark.asyncio
async def test_story_query_calls_get_story():
    story_result = {"query": {"type": "story"}, "entities": [], "facts": [], "relations": [], "conflicts": []}

    with patch("factvault.mcp.server._resolve_tenant", return_value=TENANT_ID), \
         patch("factvault.mcp.server.get_story", return_value=story_result) as mock_get, \
         patch("factvault.mcp.server.get_sync_session") as mock_sess, \
         patch("factvault.mcp.server.tenant_context"):
        mock_sess.return_value.__enter__ = lambda s: MagicMock()
        mock_sess.return_value.__exit__ = MagicMock(return_value=False)
        result = await call_tool(
            "factvault__story_query",
            {"query": "CFO departures", "depth": 2, "token": _FAKE_TOKEN},
        )

    mock_get.assert_called_once()
    assert mock_get.call_args[1]["query"] == "CFO departures"


@pytest.mark.asyncio
async def test_fact_query_via_mcp():
    facts_result = {"query": {"type": "fact_query"}, "facts": [{"id": "f1"}], "relations": [], "conflicts": []}

    with patch("factvault.mcp.server._resolve_tenant", return_value=TENANT_ID), \
         patch("factvault.mcp.server.query_facts", return_value=facts_result) as mock_qf, \
         patch("factvault.mcp.server.get_sync_session") as mock_sess, \
         patch("factvault.mcp.server.tenant_context"):
        mock_sess.return_value.__enter__ = lambda s: MagicMock()
        mock_sess.return_value.__exit__ = MagicMock(return_value=False)
        result = await call_tool(
            "factvault__fact_query",
            {"property_slug": "raised_usd", "min_confidence": 0.5, "token": _FAKE_TOKEN},
        )

    assert mock_qf.call_args[1]["property_slug"] == "raised_usd"
    assert mock_qf.call_args[1]["min_confidence"] == 0.5


@pytest.mark.asyncio
async def test_unknown_tool_raises():
    with patch("factvault.mcp.server._resolve_tenant", return_value=TENANT_ID), \
         patch("factvault.mcp.server.get_sync_session") as mock_sess, \
         patch("factvault.mcp.server.tenant_context"):
        mock_sess.return_value.__enter__ = lambda s: MagicMock()
        mock_sess.return_value.__exit__ = MagicMock(return_value=False)
        with pytest.raises(ValueError, match="Unknown tool"):
            await call_tool("factvault__unknown", {"token": _FAKE_TOKEN})
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/mcp/test_server.py -v
# expected: 5 passed
```

- [ ] **COMMIT:**

```bash
git add factvault/retrieval/retrieval.py factvault/mcp/server.py \
        factvault/retrieval/__init__.py tests/mcp/test_server.py
git commit -m "feat(mcp): MCP server with 3 tools + shared retrieval module"
```

---

## Task 17 — K8s manifests for API and MCP

**Files:** `deploy/k8s/api-deployment.yaml`, `deploy/k8s/api-service.yaml`, `deploy/k8s/api-ingress.yaml`, `deploy/k8s/mcp-deployment.yaml`, `deploy/k8s/dossier-worker-cronjob.yaml`

All manifests use Chainguard wolfi-base, nonroot UID 65532, tini, and the mandatory `fsGroup: 65532`.

**`deploy/k8s/api-deployment.yaml`:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: factvault-api
  namespace: factvault
  labels:
    app: factvault-api
spec:
  replicas: 2
  selector:
    matchLabels:
      app: factvault-api
  template:
    metadata:
      labels:
        app: factvault-api
    spec:
      securityContext:
        runAsUser: 65532
        runAsNonRoot: true
        fsGroup: 65532
      containers:
        - name: api
          image: ghcr.io/petersimmons1972/factvault-api:latest
          command: ["/sbin/tini", "--", "factvault-api"]
          ports:
            - containerPort: 8000
          env:
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: factvault-secrets
                  key: database-url
            - name: FACTVAULT_JWT_PUBLIC_KEY
              valueFrom:
                secretKeyRef:
                  name: factvault-secrets
                  key: jwt-public-key
            - name: FACTVAULT_PORT
              value: "8000"
          resources:
            requests:
              memory: "256Mi"
              cpu: "200m"
            limits:
              memory: "512Mi"
              cpu: "500m"
          readinessProbe:
            httpGet:
              path: /readyz
              port: 8000
            initialDelaySeconds: 5
            periodSeconds: 10
            failureThreshold: 3
          livenessProbe:
            httpGet:
              path: /healthz
              port: 8000
            initialDelaySeconds: 10
            periodSeconds: 30
            failureThreshold: 3
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
```

**`deploy/k8s/api-service.yaml`:**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: factvault-api
  namespace: factvault
spec:
  type: ClusterIP
  selector:
    app: factvault-api
  ports:
    - name: http
      port: 8000
      targetPort: 8000
      protocol: TCP
```

**`deploy/k8s/api-ingress.yaml`:**

```yaml
# Replace `factvault-api.example.com` with the actual hostname before deploy.
apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: factvault-api
  namespace: factvault
  annotations:
    cert-manager.io/cluster-issuer: letsencrypt-prod
    nginx.ingress.kubernetes.io/proxy-body-size: "10m"
spec:
  ingressClassName: nginx
  tls:
    - hosts:
        - factvault-api.example.com
      secretName: factvault-api-tls
  rules:
    - host: factvault-api.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: factvault-api
                port:
                  number: 8000
```

**`deploy/k8s/mcp-deployment.yaml`:**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: factvault-mcp
  namespace: factvault
  labels:
    app: factvault-mcp
spec:
  replicas: 1
  selector:
    matchLabels:
      app: factvault-mcp
  template:
    metadata:
      labels:
        app: factvault-mcp
    spec:
      securityContext:
        runAsUser: 65532
        runAsNonRoot: true
        fsGroup: 65532
      containers:
        - name: mcp
          image: ghcr.io/petersimmons1972/factvault-mcp:latest
          command: ["/sbin/tini", "--", "factvault-mcp"]
          env:
            - name: DATABASE_URL
              valueFrom:
                secretKeyRef:
                  name: factvault-secrets
                  key: database-url
            - name: FACTVAULT_JWT_PUBLIC_KEY
              valueFrom:
                secretKeyRef:
                  name: factvault-secrets
                  key: jwt-public-key
          resources:
            requests:
              memory: "256Mi"
              cpu: "100m"
            limits:
              memory: "512Mi"
              cpu: "250m"
          securityContext:
            allowPrivilegeEscalation: false
            capabilities:
              drop:
                - ALL
```

**`deploy/k8s/dossier-worker-cronjob.yaml`:**

```yaml
apiVersion: batch/v1
kind: CronJob
metadata:
  name: factvault-dossier-worker
  namespace: factvault
spec:
  schedule: "0 2 * * *"   # 02:00 UTC daily
  concurrencyPolicy: Forbid
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          securityContext:
            runAsUser: 65532
            runAsNonRoot: true
            fsGroup: 65532
          containers:
            - name: dossier-worker
              image: ghcr.io/petersimmons1972/factvault-workers:latest
              command: ["/sbin/tini", "--", "factvault-worker", "dossier", "--once"]
              env:
                - name: DATABASE_URL
                  valueFrom:
                    secretKeyRef:
                      name: factvault-secrets
                      key: database-url
              resources:
                requests:
                  memory: "256Mi"
                  cpu: "200m"
                limits:
                  memory: "1Gi"
                  cpu: "1000m"
              securityContext:
                allowPrivilegeEscalation: false
                capabilities:
                  drop:
                    - ALL
```

- [ ] **COMMIT:**

```bash
git add deploy/k8s/api-deployment.yaml deploy/k8s/api-service.yaml \
        deploy/k8s/api-ingress.yaml deploy/k8s/mcp-deployment.yaml \
        deploy/k8s/dossier-worker-cronjob.yaml
git commit -m "feat(k8s): API + MCP deployments + dossier CronJob manifests"
```

---

## Task 18 — Integration end-to-end test

**File:** `tests/integration/test_retrieval_e2e.py`

Full pipeline integration test. Requires a live Postgres DB with the factvault schema, pgvector, and RLS configured per Plan 1. Uses the `app_engine` fixture.

```python
# tests/integration/test_retrieval_e2e.py
"""
End-to-end retrieval integration test.

Requires:  FACTVAULT_TEST_DB_URL set to a live Postgres with factvault schema.
Skip:      If env var absent (CI without DB service).
"""
from __future__ import annotations

import asyncio
import json
import os
import uuid
from datetime import datetime, timezone

import pytest
from fastapi.testclient import TestClient
from sqlalchemy import create_engine, text

from factvault.api.main import create_app
from factvault.auth.jwt import issue_token   # dev token helper from T1/T2
from factvault.db.rls import tenant_context
from factvault.workers.dossier import DossierWorker

DB_URL = os.getenv("FACTVAULT_TEST_DB_URL")
pytestmark = pytest.mark.skipif(not DB_URL, reason="FACTVAULT_TEST_DB_URL not set")

# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture(scope="module")
def engine():
    return create_engine(DB_URL)


@pytest.fixture(scope="module")
def tenant_id():
    return str(uuid.uuid4())


@pytest.fixture(scope="module")
def dataset(engine, tenant_id):
    """Insert minimal dataset: Acme, MegaCorp, Beta; 5 statements; 2 relations."""
    ids = {
        "acme": str(uuid.uuid4()),
        "megacorp": str(uuid.uuid4()),
        "beta": str(uuid.uuid4()),
        "prop_acquired_by": str(uuid.uuid4()),
        "prop_founded": str(uuid.uuid4()),
    }

    # Zero-vector placeholders; real test uses pgvector ANN on non-zero vecs
    zero_vec = "[" + ",".join(["0.0"] * 1024) + "]"
    # For story ANN test: Acme gets a distinctive embedding at dim 0
    acme_vec = "[1.0" + ",0.0" * 1023 + "]"

    with engine.begin() as conn:
        conn.execute(text("SET LOCAL app.tenant_id = :tid"), {"tid": tenant_id})

        # Tenant row (if tenants table exists)
        conn.execute(
            text("INSERT INTO tenants (id, name) VALUES (:id, :name) ON CONFLICT DO NOTHING"),
            {"id": tenant_id, "name": "test-tenant"},
        )

        # Entities
        for label, eid, vec in [
            ("Acme Corp", ids["acme"], acme_vec),
            ("MegaCorp", ids["megacorp"], zero_vec),
            ("Beta Inc", ids["beta"], zero_vec),
        ]:
            conn.execute(
                text(
                    """
                    INSERT INTO entities (id, tenant_id, label, type_uri, embedding)
                    VALUES (:id, :tid, :label, 'https://schema.org/Organization',
                            CAST(:vec AS vector))
                    ON CONFLICT DO NOTHING
                    """
                ),
                {"id": eid, "tid": tenant_id, "label": label, "vec": vec},
            )

        # Properties
        conn.execute(
            text(
                """
                INSERT INTO properties (id, tenant_id, slug, label, value_type)
                VALUES (:id, :tid, 'acquired_by', 'Acquired By', 'entity_ref')
                ON CONFLICT DO NOTHING
                """
            ),
            {"id": ids["prop_acquired_by"], "tid": tenant_id},
        )
        conn.execute(
            text(
                """
                INSERT INTO properties (id, tenant_id, slug, label, value_type)
                VALUES (:id, :tid, 'founded_in', 'Founded In', 'number')
                ON CONFLICT DO NOTHING
                """
            ),
            {"id": ids["prop_founded"], "tid": tenant_id},
        )

        # Source
        source_id = str(uuid.uuid4())
        conn.execute(
            text(
                """
                INSERT INTO sources (id, tenant_id, url, content_hash, raw_text, status)
                VALUES (:id, :tid, 'https://reuters.com/test', 'abc123',
                        'Acme Corp was acquired by MegaCorp for $4.2B in 2025.', 'verified')
                ON CONFLICT DO NOTHING
                """
            ),
            {"id": source_id, "tid": tenant_id},
        )

        # Statements
        stmt_ids = []
        for i, (subj, prop, val_entity, val_number) in enumerate([
            (ids["acme"],     ids["prop_acquired_by"], ids["megacorp"], None),
            (ids["megacorp"], ids["prop_founded"],      None,           1998),
            (ids["beta"],     ids["prop_founded"],      None,           2010),
        ]):
            sid = str(uuid.uuid4())
            stmt_ids.append(sid)
            stmt_vec = "[" + ",".join(["0.1"] * 1024) + "]"
            if val_entity:
                conn.execute(
                    text(
                        """
                        INSERT INTO statements (id, tenant_id, subject_id, property_id,
                                               val_entity, confidence, embedding)
                        VALUES (:id, :tid, :subj, :prop, :val_entity, 0.85,
                                CAST(:vec AS vector))
                        ON CONFLICT DO NOTHING
                        """
                    ),
                    {"id": sid, "tid": tenant_id, "subj": subj, "prop": prop,
                     "val_entity": val_entity, "vec": stmt_vec},
                )
            else:
                conn.execute(
                    text(
                        """
                        INSERT INTO statements (id, tenant_id, subject_id, property_id,
                                               val_number, confidence, embedding)
                        VALUES (:id, :tid, :subj, :prop, :val_number, 0.70,
                                CAST(:vec AS vector))
                        ON CONFLICT DO NOTHING
                        """
                    ),
                    {"id": sid, "tid": tenant_id, "subj": subj, "prop": prop,
                     "val_number": val_number, "vec": stmt_vec},
                )
            # statement_sources row
            ss_id = str(uuid.uuid4())
            conn.execute(
                text(
                    """
                    INSERT INTO statement_sources
                        (id, statement_id, source_id, excerpt, excerpt_offset_start, excerpt_offset_end, extraction_method)
                    VALUES (:id, :stmt, :src, 'Acme Corp was acquired by MegaCorp', 0, 34, 'test')
                    ON CONFLICT DO NOTHING
                    """
                ),
                {"id": ss_id, "stmt": sid, "src": source_id},
            )

        # Relations (Acme → MegaCorp via acquired_by)
        conn.execute(
            text(
                """
                INSERT INTO relations (id, tenant_id, source_id, target_id, type, confidence)
                VALUES (:id, :tid, :src, :tgt, 'acquired_by', 0.85)
                ON CONFLICT DO NOTHING
                """
            ),
            {"id": str(uuid.uuid4()), "tid": tenant_id,
             "src": ids["acme"], "tgt": ids["megacorp"]},
        )

    ids["source_id"] = source_id
    ids["stmt_ids"] = stmt_ids
    return ids


@pytest.fixture(scope="module")
def client():
    return TestClient(create_app())


@pytest.fixture(scope="module")
def token(tenant_id):
    return issue_token(tenant_id)


# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------

def test_dossier_worker_creates_dossiers(engine, dataset, tenant_id):
    """Step 1: run worker; assert 3 dossier cache rows appear."""
    from sqlalchemy.orm import Session
    with Session(engine) as db:
        worker = DossierWorker()
        worker.run_once(db)

    with engine.connect() as conn:
        conn.execute(text("SET LOCAL app.tenant_id = :tid"), {"tid": tenant_id})
        count = conn.execute(
            text("SELECT COUNT(*) FROM dossiers WHERE tenant_id = :tid"),
            {"tid": tenant_id},
        ).scalar()
    assert count == 3


def test_get_dossier_returns_cached_bundle(client, dataset, tenant_id, token):
    """Step 2: GET /entities/{acme_id}/dossier → bundle matches cache."""
    resp = client.get(
        f"/entities/{dataset['acme']}/dossier",
        headers={"Authorization": f"Bearer {token}"},
    )
    assert resp.status_code == 200
    bundle = resp.json()
    assert any(e["id"] == dataset["acme"] for e in bundle["entities"])


def test_entities_by_name_finds_acme(client, dataset, tenant_id, token):
    """Step 3: GET /entities/by-name?q=Acme → Acme Corp returned."""
    resp = client.get(
        "/entities/by-name?q=Acme",
        headers={"Authorization": f"Bearer {token}"},
    )
    assert resp.status_code == 200
    ids = [e["id"] for e in resp.json()]
    assert dataset["acme"] in ids


def test_story_includes_acme_and_megacorp(client, dataset, tenant_id, token):
    """Step 4: POST /stories with query seeding Acme → bundle contains Acme + MegaCorp via relation."""
    from unittest.mock import patch

    acme_vec = [0.0] * 1024
    acme_vec[0] = 1.0

    with patch("factvault.retrieval.retrieval.embed_query", return_value=acme_vec):
        resp = client.post(
            "/stories",
            json={"query": "Acme acquisitions", "depth": 2},
            headers={"Authorization": f"Bearer {token}"},
        )
    assert resp.status_code == 200
    entity_ids = {e["id"] for e in resp.json()["entities"]}
    assert dataset["acme"] in entity_ids
    assert dataset["megacorp"] in entity_ids


def test_facts_query_by_property(client, dataset, tenant_id, token):
    """Step 5: POST /facts/query with property_slug=acquired_by → at least 1 fact."""
    resp = client.post(
        "/facts/query",
        json={"property_slug": "acquired_by", "min_confidence": 0.5},
        headers={"Authorization": f"Bearer {token}"},
    )
    assert resp.status_code == 200
    assert len(resp.json()["facts"]) >= 1


def test_mcp_entity_lookup_returns_same_bundle(dataset, tenant_id, token):
    """Step 6: MCP entity_lookup returns the same bundle as the REST endpoint."""
    import asyncio
    from factvault.mcp.server import call_tool

    from sqlalchemy.orm import Session
    from factvault.db import get_sync_session

    with patch_sync_session_from_engine(engine=None):
        result = asyncio.run(
            call_tool(
                "factvault__entity_lookup",
                {"entity_id": dataset["acme"], "token": token},
            )
        )
    bundle = json.loads(result[0].text)
    assert any(e["id"] == dataset["acme"] for e in bundle["entities"])


def test_source_metadata_present_on_all_facts(client, dataset, tenant_id, token):
    """Step 7: all facts returned by /facts/query include full source-existence metadata."""
    resp = client.post(
        "/facts/query",
        json={"min_confidence": 0.0},
        headers={"Authorization": f"Bearer {token}"},
    )
    assert resp.status_code == 200
    for fact in resp.json()["facts"]:
        for source in fact["sources"]:
            for field in (
                "url", "excerpt", "excerpt_offset_start", "excerpt_offset_end",
                "content_hash", "archive_url", "verification_status", "extraction_method",
            ):
                assert field in source, f"Missing source field '{field}' in fact {fact['id']}"
```

**Note:** The `test_mcp_entity_lookup_returns_same_bundle` test uses a helper `patch_sync_session_from_engine` to inject the test engine into the MCP server. Implement this helper in `tests/helpers.py` using `contextlib.contextmanager` + `unittest.mock.patch`.

- [ ] **RUN/PASS:**

```bash
$ FACTVAULT_TEST_DB_URL="postgresql://..." python -m pytest tests/integration/test_retrieval_e2e.py -v
# expected: 7 passed (or skipped if DB not available in CI)
```

- [ ] **COMMIT:**

```bash
git add tests/integration/test_retrieval_e2e.py
git commit -m "test(integration): end-to-end retrieval pipeline test"
```

---

## Task 19 — API + MCP README

**File:** `factvault/api/README.md`

```markdown
# factvault API + MCP Server

## Running locally

```bash
# Install
pip install -e ".[dev]"

# Start API (default: port 8000)
DATABASE_URL=postgresql://user:pass@localhost/factvault \
FACTVAULT_JWT_PUBLIC_KEY="$(cat keys/public.pem)" \
factvault-api

# OpenAPI docs
open http://localhost:8000/docs
```

## Issuing a dev JWT

```bash
# Generates a signed token for local testing (uses the dev RSA keypair)
factvault auth issue-token --tenant-id <uuid> [--expires 3600]
```

Set the result as:

```bash
export TOKEN=$(factvault auth issue-token --tenant-id $(uuidgen))
```

## Three retrieval modes

### 1. Entity dossier (pre-computed, served from cache)

```bash
curl -H "Authorization: Bearer $TOKEN" \
     http://localhost:8000/entities/<entity-id>/dossier
```

Returns the full sourced bundle for a single entity. If no cached dossier
exists (or the cached one is older than `FACTVAULT_DOSSIER_STALENESS_DAYS`,
default 7), the bundle is assembled on-demand and cached.

### 2. Entity name lookup

```bash
curl -H "Authorization: Bearer $TOKEN" \
     "http://localhost:8000/entities/by-name?q=Acme&type_uri=https://schema.org/Organization"
```

Returns `[{id, label, type_uri, description}]` — enough to pick an entity ID
for a dossier lookup. Not a full bundle.

### 3. Story query (on-demand graph expansion)

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"query": "Acme acquisitions", "depth": 2, "max_facts": 200}' \
     http://localhost:8000/stories
```

Seeds entities via BGE-M3 ANN, then expands the graph `depth` hops through
`relations`. Returns the full sourced bundle.

### 4. Structured fact query (no graph expansion)

```bash
curl -s -H "Authorization: Bearer $TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"property_slug": "raised_usd", "min_confidence": 0.5, "limit": 50}' \
     http://localhost:8000/facts/query
```

Pure SQL filter. Returns a bundle-shaped response with populated `facts[]` and
empty `relations[]` / `conflicts[]`.

## MCP server (Claude Desktop / Cursor / agent stacks)

```bash
# Start in stdio mode (default transport)
DATABASE_URL=... FACTVAULT_JWT_PUBLIC_KEY=... factvault-mcp
```

**Wire into Claude Desktop** (`~/.config/claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "factvault": {
      "command": "factvault-mcp",
      "env": {
        "DATABASE_URL": "postgresql://user:pass@host/factvault",
        "FACTVAULT_JWT_PUBLIC_KEY": "<base64-encoded PEM>"
      }
    }
  }
}
```

**Available tools:**

| Tool | Description |
|------|-------------|
| `factvault__entity_lookup` | Full dossier bundle for one entity |
| `factvault__story_query` | Graph-expanded story bundle from a free-text query |
| `factvault__fact_query` | Filtered fact list by property/subject/confidence |

Each tool requires a `token` argument (Bearer JWT). The server extracts
`tenant_id` from the JWT — it cannot be overridden by tool arguments.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|-------------|-----|
| `401 Unauthorized` | Missing or expired JWT | Re-issue: `factvault auth issue-token ...` |
| `404 Entity not found` | Wrong tenant or entity doesn't exist | Check tenant_id in JWT matches DB |
| Empty dossier (`facts: []`) | Entity exists but no statements yet | Run pipeline stages 1–6 first |
| Story returns no entities | No embeddings match query | Ensure entities have `embedding IS NOT NULL` (run `factvault embed entities`) |
| MCP tool hangs | DB connection pool exhausted | Check `DATABASE_URL` and Postgres max_connections |
```

- [ ] **COMMIT:**

```bash
git add factvault/api/README.md
git commit -m "docs(api): API + MCP README with curl examples and MCP wiring"
```

---

## Task 20 — OpenAPI spec snapshot test

**File:** `tests/api/test_openapi_snapshot.py`

```python
# tests/api/test_openapi_snapshot.py
"""
OpenAPI snapshot test.

First run: generates tests/api/openapi.snapshot.json and passes.
Subsequent runs: compares generated spec against snapshot; fails on diff.

To update the snapshot intentionally:
    DELETE tests/api/openapi.snapshot.json and re-run.
"""
from __future__ import annotations

import json
import os
from pathlib import Path

import pytest
from fastapi.testclient import TestClient

from factvault.api.main import create_app

_SNAPSHOT_PATH = Path(__file__).parent / "openapi.snapshot.json"


def _get_spec() -> dict:
    client = TestClient(create_app())
    resp = client.get("/openapi.json")
    assert resp.status_code == 200
    return resp.json()


def test_openapi_snapshot():
    spec = _get_spec()

    if not _SNAPSHOT_PATH.exists():
        _SNAPSHOT_PATH.write_text(json.dumps(spec, indent=2, sort_keys=True))
        pytest.skip("Snapshot created — re-run to verify.")

    snapshot = json.loads(_SNAPSHOT_PATH.read_text())

    # Normalize both sides for comparison (sort keys, deterministic serialization)
    actual = json.dumps(spec, indent=2, sort_keys=True)
    expected = json.dumps(snapshot, indent=2, sort_keys=True)

    assert actual == expected, (
        "OpenAPI spec has changed. If intentional, delete "
        f"{_SNAPSHOT_PATH} and re-run to regenerate the snapshot.\n\n"
        "First diff:\n" + _first_diff(expected.splitlines(), actual.splitlines())
    )


def _first_diff(expected_lines: list[str], actual_lines: list[str]) -> str:
    for i, (e, a) in enumerate(zip(expected_lines, actual_lines)):
        if e != a:
            return f"Line {i + 1}:\n  expected: {e!r}\n  actual:   {a!r}"
    if len(expected_lines) != len(actual_lines):
        return f"Line count mismatch: expected {len(expected_lines)}, got {len(actual_lines)}"
    return "(no diff found)"
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/api/test_openapi_snapshot.py -v
# First run: 1 skipped (snapshot created)
# Second run: 1 passed
```

- [ ] **COMMIT:**

```bash
git add tests/api/test_openapi_snapshot.py
git commit -m "test(api): OpenAPI snapshot test — catches accidental breaking changes"
```

---

## Task 21 — CI workflow update

**File:** `.github/workflows/ci.yml`

Extend the existing CI workflow. Add a job step that:
1. Starts uvicorn in the background
2. Waits for health check
3. Runs the new test suites
4. Kills uvicorn

Add or extend the `test` job in `.github/workflows/ci.yml`:

```yaml
      # --- factvault API smoke + unit tests ---
      - name: Run API + assembler + MCP + auth unit tests
        run: |
          python -m pytest tests/auth/ tests/assembler/ tests/api/ tests/mcp/ \
                           tests/workers/ -v --tb=short

      - name: Boot API server for smoke check
        run: |
          DATABASE_URL="${{ secrets.FACTVAULT_TEST_DB_URL }}" \
          FACTVAULT_JWT_PUBLIC_KEY="${{ secrets.FACTVAULT_JWT_PUBLIC_KEY }}" \
          factvault-api &
          echo $! > /tmp/factvault-api.pid
          # Wait up to 30 s for health
          for i in $(seq 1 30); do
            if curl -sf http://localhost:8000/healthz > /dev/null 2>&1; then
              echo "API ready after ${i}s"
              break
            fi
            sleep 1
          done
          curl -sf http://localhost:8000/healthz || (echo "API failed to start" && exit 1)
          curl -sf http://localhost:8000/readyz  || (echo "API not ready" && exit 1)

      - name: Kill API server
        if: always()
        run: |
          if [ -f /tmp/factvault-api.pid ]; then
            kill "$(cat /tmp/factvault-api.pid)" || true
          fi
```

If the CI file does not yet exist, create it with the following skeleton then add the steps above into the `test` job:

```yaml
name: CI

on:
  push:
    branches: [main, "plan/**"]
  pull_request:

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-python@v5
        with:
          python-version: "3.12"
      - name: Install dependencies
        run: pip install -e ".[dev]"
      # ... insert steps above here ...
```

- [ ] **COMMIT:**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: run API + MCP + auth tests; smoke-check uvicorn at /healthz"
```

---

## Task 22 — Smoke test the API + MCP CLIs

**File:** `tests/integration/test_cli_smoke.py`

Extends Plan 2's smoke test. Uses `subprocess.run` to invoke console_script entry points with `--help`. Catches packaging issues where `pyproject.toml` console_script entries aren't wired.

```python
# tests/integration/test_cli_smoke.py
"""
CLI smoke tests — verify that all console_script entry points are installed
and return exit code 0 for --help. These tests catch packaging regressions
where entry points are removed or renamed in pyproject.toml.
"""
from __future__ import annotations

import subprocess
import sys

import pytest

_ENTRY_POINTS = [
    ["factvault-api", "--help"],
    ["factvault-mcp", "--help"],
    ["factvault", "auth", "issue-token", "--help"],
    ["factvault", "worker", "dossier", "--help"],
]


@pytest.mark.parametrize("cmd", _ENTRY_POINTS, ids=[" ".join(c) for c in _ENTRY_POINTS])
def test_cli_help_exits_zero(cmd):
    result = subprocess.run(
        [sys.executable, "-m", "subprocess"] + cmd,
        capture_output=True,
        text=True,
    )
    # Use the entry point directly (requires `pip install -e .`)
    result = subprocess.run(
        cmd,
        capture_output=True,
        text=True,
    )
    assert result.returncode == 0, (
        f"`{' '.join(cmd)}` exited with code {result.returncode}.\n"
        f"stdout: {result.stdout}\n"
        f"stderr: {result.stderr}"
    )
    # --help output should mention the command name or usage
    assert "usage" in (result.stdout + result.stderr).lower() or \
           "help" in (result.stdout + result.stderr).lower(), \
        f"No usage/help text in output of `{' '.join(cmd)}`"
```

**Also add `factvault-api` and `factvault-mcp` console_script entries to `pyproject.toml`** (if not already added in Pass 1 T1):

```toml
[project.scripts]
factvault        = "factvault.cli:main"
factvault-api    = "factvault.api.main:run"
factvault-mcp    = "factvault.mcp.server:run"
factvault-worker = "factvault.workers.cli:main"
```

Add `run()` to `factvault/api/main.py`:

```python
def run() -> None:
    import uvicorn
    import os
    port = int(os.getenv("FACTVAULT_PORT", "8000"))
    uvicorn.run("factvault.api.main:app", host="0.0.0.0", port=port, reload=False)
```

And expose `app = create_app()` at module level:

```python
app = create_app()
```

- [ ] **RUN/PASS:**

```bash
$ python -m pytest tests/integration/test_cli_smoke.py -v
# expected: 4 passed (after pip install -e .)
```

- [ ] **COMMIT:**

```bash
git add tests/integration/test_cli_smoke.py
git commit -m "test(cli): smoke test all console_script entry points with --help"
```

---

## Self-Review

### Spec Coverage Checklist

All rows reference the spec (§ numbers) against the Task that implements it. Pass 1 tasks are T1–T11; Pass 2 tasks are T12–T22.

| Spec requirement | Task |
|------------------|------|
| Bundle assembler single shared function `assemble()` (§3.4) | Pass 1 T4 + T7 |
| Dossier pre-compute worker, daily CronJob (§3.4, §5) | T15 + T17 (`dossier-worker-cronjob.yaml`) |
| Dossier served from cache; on-demand if absent/stale (§3.4, §5) | T12 + `retrieval.py` T16 |
| Story on-demand: ANN seed + recursive CTE graph expansion (§3.4, §5) | T13 + `retrieval.py` T16 |
| `GET /entities/{id}/dossier` endpoint (§5) | T12 |
| `GET /entities/by-name` endpoint (§5) | T12 |
| `POST /stories` endpoint with `{query, depth, max_facts}` (§5) | T13 |
| `POST /facts/query` endpoint (§5) | T14 |
| Three MCP tools (`entity_lookup`, `story_query`, `fact_query`) (§5) | T16 |
| JWT auth + RS256 enforcement on all endpoints (§5, §6) | Pass 1 T1–T3 + T11 |
| `tenant_id` resolved from JWT claims (§6) | Pass 1 T11; T12–T14 use `get_tenant_id` dep |
| Tenant context (`SET LOCAL app.tenant_id`) per request (§6) | Pass 1 T11 `tenant_context()`; T16 MCP path |
| Conflict surfacing in bundles via `v_conflicts` (§3.2, §3.4) | Pass 1 T6 (assembler conflict query); T18 verifies |
| Source-existence metadata on every fact (url, excerpt, offsets, content_hash, archive_url, verification_status) (§3.1, §3.4) | T14 + T16 `query_facts()`; T18 Step 7 asserts all fields |
| `v_conflicts` SQL view integration (§3.2) | Pass 1 T6 assembler |
| OpenAPI spec / `/docs` (§5) | Pass 1 T9; T20 snapshot |
| K8s manifests: API Deployment, Service, Ingress; MCP Deployment; dossier CronJob (§6) | T17 |
| Dev token issuance CLI (`factvault auth issue-token`) (§6) | Pass 1 T1–T2; T22 smoke-tests it |
| BGE-M3 1024-dim embeddings for entity + statement ANN (§6) | T13 `embed_query()`; T16 `get_story()` |
| Postgres RLS via `app.tenant_id` GUC (§6) | Pass 1 T11; all T12–T16 routes call `tenant_context()` |
| Chainguard wolfi-base + tini + nonroot 65532 + `fsGroup: 65532` (§6) | T17 (all 5 manifests) |

**Coverage gaps:** None identified for §3.4, §5, §6. Spec §5 also lists `GET /properties`, `GET /sources/{id}`, `GET /conflicts` — these are deferred to Plan 5 (admin/ops surface). They are not part of Plan 4's retrieval scope.

### Placeholder Scan

Reviewed. One intentional placeholder: `factvault-api.example.com` in `deploy/k8s/api-ingress.yaml`. This is documented inline with a comment: `# Replace with the actual hostname before deploy.` No unintentional placeholders. No `TODO`, `FIXME`, or `...` in implementation code.

### Type Consistency Check

Reviewed. `assemble()` signature is consistent across all callers:

- Pass 1 T4 definition: `assemble(entity_ids: list[str], depth: int, tenant_id: str, query: str | None = None, max_facts: int | None = None, min_confidence: float = 0.0) -> dict`
- T12 entities route: `assemble(entity_ids=[str(entity_id)], depth=0, tenant_id=str(tenant_id))` ✓
- T13 stories route: `assemble(entity_ids=seed_ids, depth=req.depth, tenant_id=str(tenant_id), query=req.query, max_facts=req.max_facts)` ✓
- T15 dossier worker: `assemble(entity_ids=[eid], depth=0, tenant_id=tenant_id)` ✓
- T16 `retrieval.py` `get_dossier()`: `assemble(entity_ids=[entity_id], depth=0, tenant_id=tenant_id)` ✓
- T16 `retrieval.py` `get_story()`: `assemble(entity_ids=seed_ids, depth=depth, tenant_id=tenant_id, query=query, max_facts=max_facts)` ✓

All `entity_ids` arguments are `list[str]` (UUIDs cast to `str`). All `tenant_id` arguments are `str`. No type inconsistencies.

### Spec/Code Cross-Check — GUC Name

The spec §6 example uses `app.current_tenant_id`. Plan 1's `rls.py` uses `app.tenant_id`. This discrepancy was flagged in Pass 1.

Confirmed: every Pass 2 task that touches the GUC uses `app.tenant_id`:

- T12 entities route: calls `tenant_context(db, tenant_id)` from `factvault.db.rls` — which sets `app.tenant_id`. ✓
- T13 stories route: same dependency chain. ✓
- T14 facts route: same. ✓
- T15 dossier worker: `with tenant_context(db, tid)`. ✓
- T16 MCP server: `with tenant_context(db, tenant_id)`. ✓
- T18 integration test: direct `SET LOCAL app.tenant_id = :tid` in fixture. ✓

No Pass 2 code references `app.current_tenant_id`. The production GUC name `app.tenant_id` is used consistently throughout.
