---
name: implement-htmx-frontend
description: Use when implementing, completing, or repairing this repository's Go html/template UI, HTMX interactions, Tailwind CSS, liquid-glass styling, mobile layouts, accessibility, or frontend browser tests.
---

# Implement HTMX Frontend

## Core contract

Act as the senior frontend owner. Deliver complete, production-quality vertical slices that follow the approved architecture. Continue while safe in-scope work remains; do not stop at scaffolding, desktop-only output, compilation, or a partially working happy path.

## Establish context

1. Read `AGENTS.md` and `ARCHITECTURE.md` completely.
2. Inspect current templates, routes, view models, Tailwind configuration, JavaScript, tests, and build commands.
3. Read [references/htmx-frontend-standards.md](references/htmx-frontend-standards.md) before changing frontend code.
4. Convert the requested scope and architecture requirements into a feature/acceptance checklist. Track every item through implementation and verification.
5. Resolve only material ambiguity with the user. Do not use ambiguity to defer discoverable work.

## Implement one vertical slice at a time

1. Write the smallest browser, handler, or template test that expresses the next behavior.
2. Run it and confirm it fails for the expected missing behavior.
3. Implement the Go handler/view model, full-page template, HTMX fragment, Tailwind styling, and minimal browser behavior required by that slice.
4. Run the focused test, then relevant regression tests.
5. Inspect the result at mobile width first, then tablet and desktop.
6. Repeat until every checklist item is complete.

Keep server state authoritative. Prefer semantic HTML and normal forms that remain understandable without HTMX. Use HTMX for targeted navigation, submissions, polling, and fragment replacement. Use vanilla JavaScript only for clipboard access, secret concealment, dialog focus, and small transitions.

## Preserve project invariants

- Render full pages for ordinary navigation and fragments for HTMX requests using consistent handlers and view models.
- Keep passwords and protected values out of initial HTML. Fetch them only through explicit authorized reveal actions and apply `Cache-Control: no-store`.
- Preserve CSRF protection, server-side authorization, escaped output, and safe error messages.
- Start with unprefixed mobile Tailwind utilities; add wider breakpoint enhancements afterward.
- Provide 44×44 CSS-pixel touch targets, visible focus, semantic labels, readable contrast, no horizontal overflow, useful loading/empty/error states, and reduced-motion behavior.
- Implement the original liquid-glass design tokens and fallbacks from `ARCHITECTURE.md`; do not introduce React, Ant Design, or an imitation of proprietary Apple assets.
- Follow the installed HTMX and Tailwind versions. Verify uncertain syntax against their official documentation.

## Completion gate

Do not call work complete until:

- Every requested feature and acceptance item is implemented; a deadline is not permission to silently defer scope.
- Focused and full test suites pass, templates parse, Go builds/formats cleanly, and frontend assets build without warnings.
- Playwright covers the critical flow, HTMX swap targets/history/errors, narrow mobile, tablet, and desktop layouts.
- Keyboard use, focus, touch targets, contrast, long content, empty states, loading, expired sessions, and request failures are verified.
- No secret appears in list HTML, logs, URLs, cached responses, or unintended fragments.
- The final report names implemented features, verification commands and results, and any genuine external blocker.

When blocked, exhaust safe in-scope alternatives and preserve working state. Stop only for required user authority, unavailable external state, or a contradiction in the approved architecture; report the exact blocker and remaining checklist items.

## Red flags

- “Desktop works, so responsive work can follow.”
- “Tests or accessibility can be added later.”
- “The component compiles, so it is complete.”
- “This visual approximation is good enough without inspection.”
- “A custom JavaScript state layer is easier than server-rendered fragments.”
- “I will defer a requested feature because time is short.”

Any red flag means return to the checklist and continue implementation or verification.
