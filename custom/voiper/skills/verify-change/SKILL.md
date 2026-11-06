---
name: verify-change
description: "Verify a software change against intended behavior using relevant tests, builds, static checks, and targeted runtime checks. Use when asked to validate a diff, confirm a fix, or assess readiness; keep broad architecture and style reviews outside this workflow."
---

# Verify Change

Establish what a change is expected to do, collect proportionate evidence, and report the limits of that evidence. A successful command is useful only if it exercises the relevant change.

## Identify the change and its claims

- Use the requested commit, branch comparison, files, or established task scope. With no explicit target, inspect staged and unstaged changes and relevant untracked files. Ask for a target if none can be inferred; do not assume the latest commit is the intended change.
- Read applicable repository instructions, the diff, affected callers, and existing checks. Inspect scripts and CI configuration to discover the project's actual validation commands and prerequisites.
- Translate the intended behavior into observable outcomes. Identify affected contracts and likely regressions, including failure paths or boundary cases when the change touches them. Distinguish requested behavior from assumptions inferred from the implementation.
- Record enough of the initial working state to distinguish existing user edits from artifacts produced by verification. A verification request alone does not authorize unrelated implementation changes, commits, or deployment. Continue fixes already authorized by the surrounding task.

## Select checks that answer specific questions

Run required project checks and choose the smallest additional set that covers the meaningful risks. Connect each check to a claim rather than collecting commands for their own sake.

Read the repository's testing policy before adding tests. Prefer readable fixtures and local loopback peers for protocol or media behavior. When automated UI tests are excluded, use frontend build/static checks and manual interaction checks; do not introduce UI automation. Add application-layer tests only for a concrete risk when the local policy requires that restraint. Hardware and vendor-server checks remain unverified until actually exercised.

- Use focused existing tests for changed behavior and callers. For a bug fix, exercise the reported trigger and relevant nearby cases. When practical, show that a regression test fails against the prior behavior and passes with the fix, using an isolated copy rather than resetting the user's working tree.
- Use type checks, lint, and builds for the properties they actually establish. A build does not demonstrate correct runtime behavior, and a unit test with a mocked boundary does not establish that the real integration works.
- For changes across interfaces, generated bindings, configuration, or persistence, check compatibility at the affected boundary. Run generators or migrations only in a suitable local or disposable environment and inspect resulting artifacts.
- For user-visible behavior, exercise the relevant interaction when a runnable environment is available. Inspect rendered output for visual changes; compilation alone does not establish appearance or usability.
- Add focused regression coverage when a meaningful behavior is otherwise unprotected and the task permits edits. Avoid tests that merely duplicate implementation logic, assert private structure, or lock incidental wording. Reversible naming, formatting, or prose edits usually need existing checks or direct inspection.

## Execute and investigate

Confirm the commands target the intended checkout, configuration, and environment. Inspect unfamiliar scripts before running them if they may publish, contact live services, or mutate shared data. Prefer available local fixtures and disposable resources; missing infrastructure is a verification limit, not authorization to change a live system.

Capture exit status and the decisive output. Confirm that test discovery found the intended tests, and note skipped cases or filters that reduce coverage. Wait for launched checks to finish before calling them passed. Attribute cached or existing CI results only to the revision and configuration they actually tested.

When a check fails, distinguish a change-related defect from an environment problem, a pre-existing failure, or an unresolved cause. Establish a baseline with comparable conditions when useful and feasible; do not label a failure pre-existing merely because it looks unrelated. Avoid changing lockfiles, tool versions, fixtures, or assertions just to obtain a green result.

Retry when a changed condition or a specific hypothesis makes another run informative. Preserve evidence of intermittent failures; a later pass does not erase them. Stop repeating a blocked check when the same prerequisite is still missing, and continue independent checks that can provide useful evidence.

After an authorized fix, rerun affected checks and broaden coverage only when the new changes or unresolved risks warrant it. Once required and relevant checks pass, avoid redundant reruns. If the target changes during verification, identify which results remain applicable and rerun those invalidated by the change.

## Report the result

Lead with whether the collected evidence supports the intended behavior, reveals a defect, or leaves a material gap. Include the scope verified, relevant commands or interactions and their outcomes, and any failures, skipped checks, or unmet prerequisites. Tie actionable failures to a location or reproducible trigger where possible.

Inspect the final working state for artifacts or incidental changes produced by checks. Preserve existing user work and disclose any intentional test or implementation edits. Do not describe the change as fully verified when an essential check was blocked; explain what remains unproven and the concrete next step needed to resolve it.
