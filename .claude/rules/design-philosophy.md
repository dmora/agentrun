---
paths:
  - "session.go"
  - "errors.go"
  - "message.go"
  - "agentrun.go"
  - "engine/**/*.go"
---

# Design Philosophy: Root is Language, Backends are Dialect

See CLAUDE.md "Design Philosophy" section for the full decision rule and examples.

## Anti-Patterns

- Do NOT place cross-cutting constants in a backend because only one backend exists today. Design for N backends.
- Do NOT duplicate cross-cutting constants across backends. If two backends need the same option, it belongs in root.
- Do NOT expand Session struct fields for every cross-cutting option. Use `Option*` constants with `Session.Options`.

## Independent Control Surfaces

Root options (`OptionMode`/`OptionHITL`) and backend options (`claude.OptionPermissionMode`, `codex.OptionSandbox`) are independent:
- Root set → backend-specific option ignored
- Root absent → backend-specific option used
- Never combined or cascaded
