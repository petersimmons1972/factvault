# Frontier Models

Factvault is local-first. The default deployment should run without hosted model API keys and without sending source text, extracted facts, or embeddings outside the host.

Frontier models are opt-in for operators who explicitly want a hosted OpenAI-compatible LLM endpoint for extraction quality or latency.

## Default: Local-Only

Use local endpoints and leave API keys empty:

```bash
export FACTVAULT_LLM_URL='http://localhost:11434/v1'
export FACTVAULT_LLM_MODEL='llama3.1:8b'
unset FACTVAULT_LLM_API_KEY
```

With this configuration, extraction calls are expected to stay on the local network. The bundled embedder remains local through `FACTVAULT_EMBEDDER_URL`.

## Opt In to a Hosted OpenAI-Compatible Endpoint

Set the endpoint, model, and API key deliberately:

```bash
export FACTVAULT_LLM_URL='https://api.openai.com/v1'
export FACTVAULT_LLM_MODEL='gpt-4o-mini'
export FACTVAULT_LLM_API_KEY='...'
```

Then restart the extract worker or API process that owns the LLM client.

## Required Operator Review

Before enabling a frontier endpoint, answer these questions:

| Question | Why it matters |
|---|---|
| Is source text allowed to leave this host? | Extraction prompts may include verbatim source passages. |
| Which tenant is allowed to use the hosted endpoint? | Tenant isolation is not a substitute for data egress approval. |
| What monthly budget or request cap applies? | Frontier extraction can become expensive if workers process large queues. |
| What logs does the provider retain? | Provider retention can conflict with source handling requirements. |
| Is a fallback local endpoint configured? | Local fallback keeps extraction available during provider outages. |

## Cost Guardrails

For small deployments, start with manual batches:

```bash
./bin/factvault worker extract --tenant "$FACTVAULT_DEV_TENANT_ID" --limit 25
```

Increase the limit only after measuring request count, latency, and quality. Keep deterministic extractors enabled; they reduce the amount of text that needs hosted model review.

## Privacy Boundary

The following data may be sent to the hosted LLM when frontier extraction is enabled:

- Source title and URL
- Relevant source text excerpts
- Property vocabulary and extraction instructions
- Prior extraction context needed by the prompt

The following data should remain local unless a future implementation explicitly documents otherwise:

- Postgres credentials
- JWT signing keys
- Full database backups
- Embedding vectors and raw vector indexes

## Rollback to Local

Unset the key and point the URL back to the local endpoint:

```bash
unset FACTVAULT_LLM_API_KEY
export FACTVAULT_LLM_URL='http://localhost:11434/v1'
export FACTVAULT_LLM_MODEL='llama3.1:8b'
```

Restart workers and rerun `factvault doctor --llm-url "$FACTVAULT_LLM_URL"`.
