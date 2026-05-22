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

<!-- PASS 1 END — Pass 2 appends Tasks 12-22 + self-review below this line -->
