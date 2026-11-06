---
name: clean-code
description: "Review or refactor existing code for readability and maintainability while preserving behavior. Use for requested code cleanup, simplification, or clean-code review; avoid expanding feature work into unrelated refactoring."
---

# Clean Code

Improve how easily a maintainer can understand and change the requested code. Prefer changes with a concrete benefit over compliance with an abstract style rule.

## Establish scope and behavior

- Honor the requested mode: a review produces findings; a cleanup request permits focused edits. If no target is supplied, use the active diff or clearly established conversation scope. Ask for a target when neither exists.
- Read applicable repository instructions, nearby code, callers, and relevant tests before selecting changes. Follow established language idioms and project conventions.
- Identify observable behavior that must survive the refactor: public interfaces, returned values, errors, side-effect order, resource lifetime, and concurrency semantics where relevant. Treat discovered behavior changes as separate decisions rather than silently folding them into cleanup.

## Choose changes by their effect

- Name values for their domain meaning and units. Preserve names that are part of public APIs or serialized data unless a migration is in scope.
- Flatten nesting when it clarifies control flow, while preserving cleanup, deferred work, and side effects. A shorter expression is not necessarily easier to follow.
- Extract a function when it captures a coherent concept or isolates a responsibility. Keep related logic together when extraction would force readers to chase trivial helpers. Do not enforce arbitrary function-length or parameter-count limits.
- Prefer concrete types. Introduce an interface only for a real boundary or useful test seam; avoid wrapper layers and single-caller generic helpers that add no clarity.
- Consolidate duplication when the occurrences express the same rule and should evolve together. Keep superficially similar code separate when its domain responsibilities differ; avoid speculative abstractions and configuration switches added solely to share code.
- Make state changes and dependencies easier to see. Preserve synchronization, transaction boundaries, and evaluation order when reorganizing code.
- Remove dead code only after checking relevant callers and indirect use such as registration, reflection, generated bindings, and configuration. Absence of a direct text reference alone does not prove code is unused.
- Retain comments that explain constraints, tradeoffs, or surprising behavior. Remove stale explanations and comments that merely repeat clear code.
- Keep changes within the requested area. Formatting sweeps, dependency upgrades, framework changes, and speculative performance work need their own justification and scope.

## Verify and report

Follow the repository's testing policy. Use relevant existing checks and add focused behavior tests only where that policy and a concrete risk justify them. Keep tests as readable as production code; avoid tests tied to private structure or exact wording. Small naming or formatting changes usually need only existing checks. Simplifying protocol code must preserve interoperability and lifecycle correctness.

Inspect the final diff for accidental behavior changes and unrelated edits. Report what became clearer, which checks ran, and any unverified assumptions. In review mode, tie each actionable finding to a location and a concrete maintenance cost or failure risk; label subjective preferences as optional and accept code that needs no change.
