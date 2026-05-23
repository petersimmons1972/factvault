# Agent Comms v2 Design Synthesis

This note captures the working design agreed between Claude and Codex for
`#85` and the surrounding agent-comms loop.

## Purpose

The goal is not to invent a new coordination system. The goal is to make the
existing filesystem bus executable, predictable, and easy to extend without
semantic drift.

## Design Principle

Separate the system into three layers:

1. Protocol contract
2. Transport and queue semantics
3. Agent commands and execution policy

The protocol is the source of truth. The filesystem is only the transport.
The CLI is only the interface.

## Agreed Contract

- Keep `protocol-v1` as the wire contract.
- Do not reinterpret message fields silently.
- Keep `hmac` reserved for a later version.
- Treat Python v1 as the behavior oracle until the Go implementation matches
  external behavior.
- Use GitHub Issues as the durable work ledger.
- Use the bus for realtime coordination, clarifications, and acknowledgements.

## Roles

- Claude coordinates: ordering, prioritization, and design decisions.
- Codex executes: implementation, tests, and concrete repository changes.
- If a task is ambiguous, the right response is a `question`, not a guess.
- If a task cannot proceed, the right response is a `block` with a concrete
  reason.

## Command Model

The CLI should expose two layers of behavior:

### Core bus operations

- `send`
- `read`
- `ack`
- `heartbeat`
- `archive`
- `health`

### Orchestration verbs built on the bus

- `claim`
- `handoff`
- `question`
- `block`
- `capability publish`
- `lessons list|get|propose`

The orchestration verbs are not separate transport concepts. They are
structured actions that produce or consume bus messages and issue state.

## Queue Semantics

The transport must stay deterministic under load:

- Atomic writes via `.tmp` plus rename
- Hard queue cap
- Soft cap for backpressure signaling
- Priority drain for blocking messages
- Dead-letter routing for malformed messages
- Per-recipient cursor support for unread consumption
- FIFO behavior within each sender lane

The queue is not “best effort”. If a message cannot be validated, it should be
rejected, routed, or surfaced explicitly.

## State Machine

The visible coordination loop should remain simple:

`order -> claim -> progress -> handoff`

Supporting signals:

- `heartbeat` proves liveness
- `question` requests clarification
- `block` stops execution until resolved
- `ack` confirms receipt

## Negotiation Boundaries

- Keep ambiguity resolution tight.
- Use the bus for short clarification loops.
- If a design choice cannot be resolved quickly, escalate it to the issue
  thread instead of hard-coding an assumption.
- Do not let negotiation mutate the protocol implicitly.

## Current Go v2 Position

The current Go implementation already covers the core bus mechanics:

- send
- read
- ack
- heartbeat
- archive
- health

The next step is to finish the semantics that make the bus a reliable
coordination layer rather than just a file writer:

- unread cursor behavior
- claim / handoff flows
- question / block handling
- capability publication
- lessons storage and retrieval

## Non-Goals

- Network transport
- HMAC enforcement in v1
- Cross-host routing
- Silent compatibility shims that hide protocol drift

## Decision Summary

The system should behave like this:

- Claude decides what should happen next.
- Codex implements the next concrete step.
- The bus carries the coordination.
- The issue tracker records the durable backlog.
- The Go binary must match the protocol, not invent a new one.

