# factvault

> A self-hostable research database where every fact is grounded in a verifiable, durably-archived source.

factvault captures facts from web sources, archives the originals (full body + Wayback snapshot + content hash), extracts structured statements with verbatim source excerpts, computes confidence from cross-source corroboration, and assembles the result into JSON bundles your LLM can use to write long-form output without hallucinating.

## Status

**Pre-implementation.** The design spec is in [`docs/superpowers/specs/2026-05-22-factvault-design.md`](docs/superpowers/specs/2026-05-22-factvault-design.md). No code yet.

## The two payload shapes

- **Dossier** — answers *"tell me about X"*. Pre-computed per entity, refreshed on schedule.
- **Story** — answers *"tell me what's going on with this idea"*. Assembled on demand from a query, expands the graph N hops.

Both share one bundle format and carry full source-existence metadata on every fact: URL, verbatim excerpt, content hash, Wayback archive URL, verification timestamp, verification status.

## License

MIT.
