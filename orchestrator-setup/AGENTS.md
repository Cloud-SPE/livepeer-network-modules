# Orchestrator setup

Standalone broker file generation only. No Docker socket, deployment, transactions,
cold private keys, or manifest publication. Inputs are read-only; generated hot
material is journaled and preserved on reruns. Never replace corrupt material.

Use actual broker parser and key-generation binaries. Compose validation must run
offline. State exactly which live checks remain unperformed. Run `make test` and
the built-container acceptance test with synthetic input before completing changes.
Use beads for work tracking. Runtime services receive only their own secrets.
