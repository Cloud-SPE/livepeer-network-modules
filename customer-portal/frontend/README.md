# `customer-portal/frontend/`

pnpm sub-workspace per OQ1 lock. Three packages:

- `shared/` — shared widget catalog (Lit + RxJS, TypeScript). Per FQ4 +
  Q5 + OQ4 locks: ships `.ts` sources plus a pre-built `dist/` artifact.
  Per-product portals/admins consume the dist as an ESM dep.
- `portal/` — customer-facing SPA scaffold consuming `shared/`.
  Per-product portals (openai, vtuber, video) extend.
- `admin/` — operator SPA scaffold consuming `shared/`. Per-product
  admins extend.

OQ4 lock: `shared/` ships pre-bundled; `portal/` + `admin/` are starter
scaffolds — per-product Vite builds bundle the final SPA.
