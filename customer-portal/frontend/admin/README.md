# `customer-portal/frontend/admin/` — scaffold

Operator-facing SPA scaffold. Per OQ4 lock, this directory is a starter
template — per-product admins (openai-admin, vtuber-admin, video-admin)
extend it with their own routes and Vite config.

## Routes

- `#/customers` — operator search + customer list
- `#/customers/:id` — customer detail (balance, status, audit)
- `#/topups` — top-up history + manual refund
- `#/audit` — admin audit feed
