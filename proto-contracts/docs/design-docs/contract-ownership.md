---
title: Contract ownership
status: accepted
last-reviewed: 2026-05-05
---

# Contract ownership

`proto-contracts/` is the single owner of canonical in-monorepo wire
contracts. Producers and consumers both import generated stubs from this peer
module rather than owning private copies.
