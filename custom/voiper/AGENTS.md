# Voiper — Linux SIP client

Build a practical Linux desktop softphone with NixOS as the primary target. Keep ordinary SIP independent of Swyx extensions. Go owns calls, audio, credentials, and persistence; the Wails/Svelte UI presents state and sends commands.

## Implementation

- Read surrounding code before editing and preserve good existing conventions.
- Prefer simple control flow, descriptive names, focused functions, and concrete types.
- Keep related behavior together. Avoid trivial wrappers, needless layers, and generic helpers for a single caller.
- Introduce interfaces only for a real boundary or useful test seam. Prefer a little duplication over premature abstraction.
- Reuse maintained SIP, media, and codec libraries. Keep reusable packages independent of the UI and application configuration; isolate vendor behavior in its adapter.
- Comment decisions, constraints, and non-obvious behavior. Do not narrate obvious code.
- Make socket, device, goroutine, and channel ownership explicit. Do not hold application locks while waiting on the network or devices. Keep queues bounded.
- Preserve protocol correctness when simplifying. Never claim unsupported Swyx behavior works without evidence.
- Review the final diff for unnecessary comments, helpers, interfaces, indirection, awkward names, and unrelated changes.

## Verification

- Write simple unit and end-to-end tests for SIP and audio/media libraries.
- Do not implement automated UI tests. Build/check the frontend and verify interaction manually when a desktop is available.
- Add other backend tests only for a concrete risk, such as cancellation, persistence, migration, or isolation between calls/accounts.
- Test code must be as clear as production code. Avoid elaborate test frameworks and assertions tied to private structure.
- Report checks run and limitations honestly. Hardware and real-server interoperability require actual evidence.

## Collaboration

Once shared APIs are agreed, independent work may run in parallel when the user requests it. Assign file ownership, coordinate dependency changes, and preserve other contributors' edits.
