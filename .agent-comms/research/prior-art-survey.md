# Prior-Art Multi-Agent Communication Survey

**Research Date**: May 23, 2026  
**Scope**: Multi-agent communication protocols, emphasis on systems released 2024-2026

---

## Executive Summary: Top Finding

**Google's A2A (Agent-to-Agent) Protocol v1.0** is the most relevant established system for our use case. Donated to Linux Foundation in April 2025 and now production-ready (v1.0 released March 2026), A2A operates at agent-to-agent scope (not tool-to-agent like MCP), emphasizes capability discovery via Agent Cards, supports multiple transports (HTTP/JSON-RPC, SSE streaming, gRPC), and includes enterprise authentication and audit hooks. Unlike our filesystem-only v0 design, A2A already solves heartbeat, liveness, and asynchronous task negotiation at scale with 150+ organizations adopting it. Its constraint: requires HTTP endpoints and does not assume co-location; our use case (Claude↔Codex on one box) is simpler.

---

## Established Systems Comparison Table

| Name | Transport | Capability Discovery | Heartbeat/Liveness | Audit/Observability | License | Maturity | Key Strength |
|------|-----------|----------------------|-------------------|---------------------|---------|----------|--------------|
| **A2A (Google)** | HTTP(S)/JSON-RPC, SSE, gRPC | Agent Cards (JSON schema) | Yes, task lifecycle management | Request/response tracing, task IDs | Apache 2.0 | Production (v1.0, Mar 2026) | Enterprise-grade, 150+ orgs, Linux Foundation backing |
| **MetaGPT** | Pub/Sub message queue (in-process) | Not explicit; message typing | Implicit via message flow | Environment broadcasts all messages | MIT | Stable (v0.8+) | Structured message protocol with role-based interests, publish-subscribe |
| **CrewAI** | In-process task delegation, A2A client | Tools/capabilities registered per agent | Implicit via task completion tracking | Extensive telemetry integration (Langfuse, Datadog, etc.) | Apache 2.0 | Stable (v1.14+) | Native A2A support, flexible LLM-driven delegation |
| **Microsoft AutoGen** | Direct method calls, SSE | Method signatures exposed | Conversation state tracking | Built-in logging, conversation history | Apache 2.0 | Stable (v0.4+) | Conversational agent-to-agent via group chat |
| **LangGraph** | Graph state transitions (in-process) | Not explicit; graph topology | Not explicit; relies on graph execution | Event system, checkpointing | MIT | Stable | Structured control flow, state persistence |
| **FIPA-ACL** | TCP/IP messaging (ACL bit syntax) | Agent Directory Service (DF) | Keep-alive, subscription lifecycle | Message archiving via specs | Open (W3C heritage) | Legacy (90s standard, inactive) | Foundational formal semantics, agent naming/routing |

---

## Indy Dev Dan's Multi-Agent Work

**Online Handle**: @indydevdan | **GitHub**: github.com/disler | **Blog**: indydevdan.com

Key content (last 6 months):
1. **"Claude Code Multi-Agent Orchestration with Opus 4.6, Tmux and Agent Sandboxes"** (YouTube, Feb 2026)
   - Focuses on spawning independent agents with isolated tmux sessions
   - Advocates for task systems for "reliable agent-to-agent communication" 
   - Emphasizes builder + validator agent pairs

2. **"Pi to Pi: Two-Way Agent Orchestration with the Pi Coding Agent"** (YouTube, May 2026, 5 days old)
   - Claims subagents are "LOCAL MAXIMUM" of multi-agent orchestration
   - Explores two-way agent coordination patterns beyond subagent nesting

3. **"Claude Code Task System: ANTI-HYPE Agentic..."** (YouTube, Feb 2026)
   - Advocates task system for agent-to-agent communication reliability
   - Builder/validator pattern paired with task system

4. **Repos**: 
   - `multi-agent-postgres-data-analytics` (874 stars) — practical multi-agent example
   - Focus on reproducible, local-first patterns (avoid API key leakage)

**Alignment with our spec**: Dan's work emphasizes **task-driven communication** and **builder/validator patterns** over raw message passing. No published protocol; focuses on patterns and tooling around Claude Code's native features.

---

## What We Should Steal (Pattern Lessons)

1. **Agent Cards for Capability Discovery**: A2A's Agent Card (JSON schema exposing skills, input/output modes, security schemes) prevents agents from querying "can you do X?" — instead they advertise what they can do upfront. Lightweight compared to full WSDL, practical for both local and network scenarios.

2. **Publish-Subscribe Typing** (MetaGPT): Message routing by `cause_by` class reduces tight coupling compared to direct method calls. Agents subscribe to message *types*, not agents. Scales from single-process to distributed without refactor.

3. **Builder/Validator Agent Pairs** (Indy Dev Dan pattern): Two independent agents with clear contract (builder produces, validator checks) is simpler than three-agent consensus loops. Aligns with our filesystem-bus v0 design where agents write outputs and peers read/validate.

4. **Task Lifecycle Tracking**: A2A's explicit task IDs, status enums (pending/running/completed/failed), and conversation turn limits prevent runaway loops and enable observability. Cheaper than full tracing; richer than implicit timeouts.

5. **Asynchronous Handoff with Streaming**: A2A's Server-Sent Events (SSE) path avoids polling. If v1 scales to network, streaming replaces polling automatically.

---

## What We Should NOT Steal

1. **Network-First Architecture**: A2A assumes agents are network endpoints; firewall, DNS, certificate management are required. For our v0 (two agents on one box), this is overkill. Defer to v1 network-bus when we need it.

2. **Full OAuth2 / Complex Auth**: A2A supports Bearer tokens, OAuth2 client credentials, HTTP Digest. For local filesystem bus, simple opaque tokens or Unix permissions are sufficient. We can layer auth later.

3. **gRPC Transport**: A2A offers gRPC as option (enterprise customers use it). For v0, HTTP overhead is negligible; gRPC adds protobuf complexity. JSON-RPC or simple REST is simpler.

4. **Form-Based UX Negotiation**: A2A allows agents to negotiate input/output modalities (text, JSON, forms, media types) dynamically. Useful for heterogeneous agent ecosystems. Our homogeneous Claude↔Codex pair can assume JSON always.

5. **FIPA-ACL Formal Semantics**: FIPA has rigorous agent naming, conversation IDs, and protocol state machines. Beautiful, mature, but 25+ years of complexity for an 80/20 problem. The formalism doesn't improve our two-agent case.

6. **Heavyweight Service Registries**: CrewAI and A2A both support agent discovery services (agent directories, registries). For v0, hardcoded endpoints or environment variables are sufficient.

---

## Open Questions for Founder

### 1. Should we adopt A2A wholesale and skip our custom protocol?

**Argument for adoption**:
- A2A is production-ready, battle-tested, Linux Foundation-backed, and covers heartbeat/liveness/audit.
- If we ever need multi-framework interop (CrewAI agents alongside Claude), A2A is the standard.
- Implementing A2A compliance is straightforward (expose an Agent Card endpoint, handle JSON-RPC task requests).
- Our filesystem v0 can be a transport *underneath* A2A (A2A messages serialized to filesystem bus, deserialized by peer).

**Argument against**:
- A2A assumes network endpoints; our v0 is local, tight-coupled. Overhead.
- Custom protocol lets us encode our specific patterns (builder/validator, cost negotiation, worktree isolation) from day one.
- We can implement A2A *later* as a wrapper (backward-compatible if we design v0 well).

### 2. Which patterns from prior art are non-negotiable?

**Must have**:
- Agent Cards / capability advertisement (avoids "can you do X?" queries)
- Task ID and lifecycle tracking (observability, loop prevention)
- Publish-subscribe message typing (decouples agents)

**Nice-to-have**:
- Authentication (add in v1 network-bus)
- Streaming/polling update mechanisms (add in v1 network-bus)

### 3. Do we need structured output negotiation (JSON schema in messages)?

MetaGPT and A2A both allow clients to embed a Pydantic/JSON schema in outgoing messages, asking the server to return structured data matching that schema.

**Pros**: Type safety, clearer contracts.  
**Cons**: Adds message complexity; assumes server-side schema validation (not always desired).

For v0, try **optional schema hints** (agent can ignore if they prefer to return raw text).

---

## Recommendation: Hybrid Approach

1. **Design v0 custom protocol** using the steal-this patterns (Agent Cards, publish-subscribe typing, task lifecycle IDs, builder/validator).
2. **Plan v0→v1 migration path** so we can bolt on A2A or MCP later without breaking existing agents.
3. **Reserve protocol version field** in messages for future expansion to A2A-compliant mode.
4. **Build one A2A reference implementation** (Claude agent exposed as A2A server) early, to validate interop assumptions before network-bus v1 launch.

---

## Sources Reviewed

1. **A2A Protocol v1.0 Specification** — https://a2a-protocol.org/latest/
2. **Google A2A GitHub** — https://github.com/a2aproject/A2A (23.9k stars, 562 commits)
3. **CrewAI A2A Integration Docs** — https://docs.crewai.com/en/learn/a2a-agent-delegation
4. **MetaGPT Agent Communication Guide** — https://docs.deepwisdom.ai/main/en/guide/in_depth_guides/agent_communication.html
5. **LangGraph Multi-Agent Patterns** — Multiple tutorials and community discussions (pub-sub via graph state)
6. **Indy Dev Dan YouTube Channel** — Multi-agent coordination advocacy (last 6 months)
7. **Academic Papers**: "A Semantic View of Agent Communication Protocols" (arXiv 2026), "MetaGPT: Meta Programming for A Multi-Agent Collaborative Framework" (ICLR 2024)

---

**Compiled by**: Research Agent  
**Deliverable**: `/home/psimmons/projects/factvault/.agent-comms/research/prior-art-survey.md`
