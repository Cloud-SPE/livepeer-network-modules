# member-portal

Follow root AGENTS.md and the accepted regional-pools design. This component
owns wallet sign-in, opaque sessions and presentation only. Regional controllers
remain authoritative for membership, GPU ownership, terms and money. Never add
operator credentials, payout keys or a regional accounting database here.

Use Docker-first build/test/run surfaces. Keep browser tokens server-side,
allowlist regional member operations, pin HTTPS origins, and enforce exact
Origin checks on browser mutations. Follow root frontend DOM/CSS invariants.
