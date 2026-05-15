# Agent guidance — daydream-portal

## What this package is
SaaS shell in front of `daydream-gateway`. Waitlist → admin approval →
API key → Lit playground SPA that opens Scope sessions through the
gateway. Ported (lift-and-strip) from `daydream-live-pipelines`.

## What this package is NOT
- Not a payments processor. No Stripe wiring; `customer-portal` Stripe
  paths are deliberately left unmounted. Do not re-introduce them.
- Not a content platform. No projects, workflows, plugins galleries,
  comments, follows, bookmarks. Do not re-introduce them.
- Not a moderation surface. No reports, no BullMQ admin.
- Not a media path. WebRTC happens directly between the browser and
  the orchestrator; this package only logs session lifecycle.

## When you change things
- New tables go in `migrations/` AND `src/db/schema.ts`. Keep the
  drizzle schema and the SQL in lock-step.
- Public routes mount on `/portal/*`, admin on `/admin/*`. Public
  reads default to anonymous; everything else requires UI token
  (customer) or `X-Admin-Token`+`X-Admin-Actor` (operator).
- New auth concerns probably belong upstream in `customer-portal/`;
  reach there first before adding a parallel auth path here.
- The Lit SPAs compose `@livepeer-network-modules/customer-portal-shared`
  widgets first; only add bespoke components for daydream-specific UX
  (`portal-daydream-playground`, `portal-daydream-prompts`).
