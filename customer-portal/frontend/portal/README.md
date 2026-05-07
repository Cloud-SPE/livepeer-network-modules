# `customer-portal/frontend/portal/` — scaffold

Customer-facing SPA scaffold. Per OQ4 lock, this directory is a starter
template — per-product portals (openai-portal, vtuber-portal,
video-portal) extend it with their own routes and Vite config.

## Routes

- `#/signup` — render `<portal-signup>`
- `#/login` — render `<portal-login>`
- `#/account` — render balance + nav
- `#/api-keys` — render `<portal-api-keys>`
- `#/billing` — render `<portal-checkout-button>`

See `src/index.ts` for the bootstrap pattern.
