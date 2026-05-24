---
plan: 0037
title: Align secure-orch-console and orch-coordinator operator UX with a shared shell and design language
status: active
phase: implementation
opened: 2026-05-24
owner: harness
related:
  - "completed plan 0018 — orch-coordinator design"
  - "completed plan 0019 — secure-orch trust spine design"
  - "completed plan 0023 — strict frontend DOM and CSS invariants"
---

# Plan 0037 — align secure-orch-console and orch-coordinator operator UX with a shared shell and design language

## 0. Why this plan exists

The two operator-facing UIs already solve adjacent workflows, but they do not yet read
as one product family. Both are server-rendered and functional, yet they still expose
their internals as plain sections and forms rather than an intentional operator shell.

The target is not a framework rewrite. The target is a shared operator UX contract:

- the same navigation shell
- the same visual tokens and component primitives
- the same feedback states
- task-oriented page hierarchy for review, publish, and audit work

## 1. Constraints

- Keep both UIs in light DOM only.
- Keep styling in checked-in CSS files only.
- Preserve the existing Go `html/template` + embedded asset architecture.
- Use JavaScript only where it materially improves UX without introducing a frontend
  build step.
- Do not collapse the distinct security postures or operator workflows of the two
  components.

## 2. First-pass implementation scope

This slice ships the shared shell and styling baseline, not the full information
architecture rewrite.

### 2.1 Shared shell

- Add the same topbar + sidebar + content shell to both UIs.
- Support a mobile drawer sidebar via a tiny embedded JS helper.
- Expose consistent active navigation treatment across pages.

### 2.2 Shared design primitives

- Introduce aligned CSS tokens for:
  - background and surface colors
  - accent, info, warning, and danger states
  - spacing, radius, borders, and typography
- Restyle:
  - cards
  - buttons
  - badges and status pills
  - forms
  - tables
  - code blocks
  - alerts and flash messages

### 2.3 First-pass page hierarchy cleanup

- Reframe existing page headings and explanatory copy so the workflow reads as:
  - `secure-orch-console`: inspect protocol state, perform explicit protocol actions,
    review manifest diff, confirm signing, audit operator activity
  - `orch-coordinator`: inspect roster health, review candidate drift, upload the
    signed manifest, audit publication history
- Preserve existing routes and handler behavior.

## 3. Follow-on work intentionally left out of this slice

- New overview/dashboard routes
- richer inline filtering and sorting
- copy-to-clipboard helpers
- diff row expansion controls
- audit search
- cross-component shared asset package

Those can land incrementally after the shell and visual system are stable.

## 4. Success criteria

This plan is complete when:

- both UIs share the same operator shell structure
- both UIs use the same visual token vocabulary and component primitives
- navigation, forms, status messaging, and tables feel obviously related
- the changes remain compliant with repo-wide frontend DOM and CSS invariants
