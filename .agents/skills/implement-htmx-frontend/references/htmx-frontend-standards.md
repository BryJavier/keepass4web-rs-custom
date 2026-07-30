# HTMX Frontend Standards

## Template and response contracts

- Keep layout, page, component, and fragment templates distinct.
- Give each fragment one stable root element and ID when it is a swap target.
- Build view models in Go; do not place domain decisions inside templates.
- Escape user-controlled text through `html/template`. Avoid `template.HTML` unless content is generated and audited.
- Render the same underlying component from full-page and HTMX responses to prevent drift.
- Return semantic HTML for validation, empty, loading, permission, conflict, and failure states.
- Use Post/Redirect/Get for successful non-HTMX forms. For HTMX, return the updated fragment or an explicit redirect/history response.
- Select one tested convention for validation errors: either swappable `200` responses or configured swapping for `422`. Do not return an error body HTMX will ignore.

## HTMX syntax checklist

For every interaction, verify:

1. The HTTP method and URL match a real Go route.
2. `hx-target` identifies exactly one stable container.
3. `hx-swap` preserves valid DOM structure and focus behavior.
4. Request parameters include CSRF and the current record revision when state changes.
5. Loading and disabled states prevent accidental duplicate submissions.
6. History updates are intentional; back/forward navigation reproduces valid content.
7. Errors produce visible, accessible feedback.
8. Elements introduced by a swap initialize correctly without a global client state store.

Prefer:

```html
<form
  method="post"
  action="/entries/{{ .Entry.ID }}/edit"
  hx-post="/entries/{{ .Entry.ID }}/edit"
  hx-target="#entry-detail"
  hx-swap="outerHTML"
>
  <input type="hidden" name="csrf_token" value="{{ .CSRFToken }}">
  <input type="hidden" name="revision" value="{{ .Entry.Revision }}">
  <!-- labeled fields and inline errors -->
  <button type="submit">Save entry</button>
</form>
```

Keep the ordinary `method` and `action` as progressive-enhancement semantics even when the HTMX response is optimized as a fragment.

## Mobile-first Tailwind rules

- Use base utilities for the narrow layout; add `sm:`, `md:`, and larger changes only when content needs them.
- Test at 320–375px before wider viewports.
- Avoid fixed widths for content surfaces. Use bounded `max-w-*`, flexible grids, `min-w-0`, wrapping, and deliberate truncation.
- Never rely on hover alone. Supply touch, keyboard, focus, and active states.
- Keep destructive and primary actions reachable without covering content or unsafe-area insets.
- Verify long titles, URLs, usernames, notes, validation text, and translated-length labels.
- Avoid large blur regions and unnecessary layered transparency on mobile.

## Liquid-glass system

- Express color, opacity, blur, borders, radii, shadows, and spacing as shared tokens.
- Reserve translucent glass for navigation and major surfaces; use calmer content surfaces for readability.
- Provide a solid or near-opaque fallback through ordinary CSS before any `@supports (backdrop-filter: blur(...))` enhancement.
- Maintain readable contrast over every background state.
- Respect `prefers-reduced-motion`; keep transitions short and functional.
- Create an original visual implementation. Do not copy Apple artwork, exact product layouts, or proprietary assets.

## Accessibility and security

- Use landmarks, headings in order, native buttons/links, associated labels, status regions, and dialog focus trapping/restoration.
- Keep tap targets at least 44×44 CSS pixels and focus rings visible.
- Announce async success/error changes appropriately without moving focus unexpectedly.
- Conceal secrets by default and remove revealed values from the DOM after the defined timeout.
- Never put secrets in query strings, element IDs, analytics, logs, or client persistence.
- Prevent caching on secret-bearing responses and test authorization on every reveal/download endpoint.

## Verification matrix

Check each completed flow against:

| Dimension | Minimum evidence |
| --- | --- |
| Behavior | Focused handler/template test and Playwright critical path |
| HTMX | Correct target, swap, history, loading, validation, and failure behavior |
| Responsive | Narrow mobile, tablet, and desktop screenshots or inspection |
| Input | Keyboard, touch, long content, duplicate action, stale revision |
| Accessibility | Labels, landmarks, focus, contrast, reduced motion, status feedback |
| Security | No secret leakage; CSRF and authorization enforced |
| Build | Go format/build/tests and Tailwind asset build pass cleanly |
