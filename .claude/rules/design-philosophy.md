---
paths:
  - "session.go"
  - "errors.go"
  - "message.go"
  - "agentrun.go"
  - "engine/**/*.go"
---

# Design Philosophy: Root is Language, Backends are Dialect

When adding or modifying constants, types, or options, apply this decision rule:

> **Would this concept exist if backend X didn't exist?**
> - **Yes** → it belongs in the root `agentrun` package.
> - **No** → it belongs in the backend package (e.g., `engine/cli/claude/`).

## Quick Reference

**Root (shared vocabulary):**
- `MessageType` constants — output vocabulary (what agents say)
- `Option*` constants — input vocabulary (what you ask of agents)
- `Mode`, `HITL` types — cross-cutting session controls
- `Session.Model`, `Session.Prompt` — structural config

**Backend (dialect):**
- Wire format mapping (CLI flags, API bodies)
- Backend-specific options (`OptionPermissionMode`)
- Backend-specific permission/mode constants

## Anti-Patterns

- Do NOT place cross-cutting constants in a backend because only one backend exists today. Design for N backends.
- Do NOT duplicate cross-cutting constants across backends. If two backends need the same option, it belongs in root.
- Do NOT expand Session struct fields for every cross-cutting option. Use `Option*` constants with `Session.Options`.

## Independent Control Surfaces

Root options (`OptionMode`/`OptionHITL`) and backend options (`claude.OptionPermissionMode`, `codex.OptionSandbox`) are independent:
- Root set → backend-specific option ignored
- Root absent → backend-specific option used
- Never combined or cascaded
