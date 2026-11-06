---
name: architecture-review
description: "Review software architecture in a repository, proposed design, or change for boundary, dependency, data ownership, and operational risks. Use for architecture assessments and design tradeoff reviews; ordinary style cleanup and line-level bug review do not require this skill."
---

# Architecture Review

Assess whether the design supports the system's actual requirements and expected changes. Ground recommendations in evidence and consequences, rather than a preferred architecture pattern.

## Set the review boundary

- Use the user's specified repository, subsystem, proposal, or diff. For a change review, inspect its integration points without turning it into a whole-system audit. If there is no established target, ask for one.
- Read applicable repository instructions and design decisions. Identify relevant constraints such as deployment model, compatibility, workload, availability, team ownership, and migration cost. Distinguish documented requirements from assumptions; ask only about unknowns that materially affect a recommendation.
- A request to review authorizes inspection and findings. Implement changes only when requested, and preserve the user's chosen technology and scope.

## Establish how the system works

Trace representative flows from an entry point through business logic, state, and external dependencies. Inspect implementations and callers as well as documentation; folder names and diagrams alone do not establish enforced boundaries.

Build only the map needed for the review: component responsibilities, dependency direction, data ownership, runtime or deployment boundaries, and critical external contracts. Distinguish compile-time dependencies from runtime calls, events, and shared state. A compact diagram is useful when these relationships are difficult to describe in prose; label proposed relationships separately from observed ones.

For proposals without implementation, evaluate the stated design and identify the contracts or experiments needed to resolve uncertainty. Do not report hypothetical implementation defects as observed findings.

## Examine consequential tradeoffs

Choose the dimensions relevant to the target; do not force every review through an exhaustive checklist.

- **Boundaries and change propagation:** Look for responsibilities that change for different reasons, cycles that force coordinated changes, and abstractions that expose another component's internals. Explain which realistic change becomes harder. Shared code or a large module is not a defect by itself.
- **State and contracts:** Identify the source of truth, write ownership, consistency requirements, transaction boundaries, and compatibility expectations. Across asynchronous boundaries, consider duplicates, ordering, retries, and idempotency only where delivery behavior makes them relevant.
- **Failure and resource ownership:** Trace the effects of an unavailable dependency, partial completion, cancellation, shutdown, and resource exhaustion where applicable. Check whether responsibility for recovery and cleanup is explicit. Avoid recommending retries without considering duplicate effects and load amplification.
- **Deployment and evolution:** Assess whether components that must change together can be deployed compatibly, and whether schema or contract migrations allow recovery. Consider rollback limits when writes or external effects cannot be reversed.
- **Operational evidence:** Connect performance and reliability concerns to workload assumptions, measured behavior, or concrete resource limits. Flag missing evidence instead of asserting bottlenecks. Evaluate whether critical failures can be detected and diagnosed.
- **Testability and trust boundaries:** Examine whether important contracts and failure paths can be exercised without reproducing the entire system. Address authorization or isolation boundaries when they affect the architecture; keep a full security audit outside scope unless requested.

Prefer the smallest change that resolves an evidenced problem. Compare a recommendation with retaining the current design, and explain its cost as well as its benefit. Do not prescribe microservices, additional layers, repositories, queues, or framework replacements merely to match a pattern.

Prefer concrete resource ownership over new layers: identify who closes sockets, devices, workers, and channels, whether queues are bounded, and whether slow work holds shared locks. Keep vendor extensions outside generic protocol packages. A small adapter with a demonstrated responsibility is sufficient; do not recommend a plugin framework simply because two providers differ. Follow repository-specific testing constraints when proposing verification.

## Deliver actionable findings

Lead with the most consequential supported findings. For each finding, provide the relevant code location or design section, the triggering scenario, the consequence, and a proportionate recommendation. Rank by impact and likelihood under stated requirements; keep uncertain risks and open questions distinct from confirmed problems.

For substantial recommendations, describe an incremental path, compatibility constraints, and how success could be verified. Recommend a targeted experiment or measurement when evidence is insufficient to choose a design. Run relevant existing checks only when they help resolve a review question; passing tests alone does not establish architectural fitness.

Close with the scope inspected, material evidence gaps, and decisions needed from the user. Mention sound existing choices when they affect the recommendation, and state plainly when no significant issue is supported. Do not manufacture findings or present a sample of inspected flows as exhaustive coverage.
