# Local setup-container validation — 2026-09-17

Built `livepeer-pool-setup:local`, image ID
`sha256:76b43ad0d004a6cb460636f23322a294648363cb4d7560ed116c7a30b68232ba`.
No image was pushed. Source changes were uncommitted during this validation.

Evidence:

- `python3 -m unittest discover -s pool-setup/tests -v`: 7 tests passed;
  `/tmp/pool-setup-unit-final.log`.
- `python3 pool-setup/tests/container_acceptance.py`: passed;
  `/tmp/pool-setup-acceptance-final.log`.
- Fixture directory: `/tmp/pool-setup-acceptance-hypt_znl`.
- Image build: `/tmp/pool-setup-build-final.log`.
- Both operator setup Compose files and generated runtime Compose passed the
  actual Docker Compose configuration parser.
- `bash -n infra/scripts/build-images.sh` passed.
- Image's `skopeo` resolved the public member-agent tag through registry TLS;
  `/tmp/pool-setup-registry-probe.log`. This was metadata access only.

Container acceptance generated actual encrypted V3 receiver/payout wallets,
broker keys, portal issuer/trust and internal ownership TLS. It initialized a
synthetic controller's persistent identity using the actual controller binary.
The controller, broker, reconciler, executor and portal configuration parsers
passed. No daemon was started as a service, and no financial wallet was unlocked
for transaction submission.

Verified behaviors: read-only plan; identical reruns preserve every material
file and package revision; import of the earlier operator directory layout and
existing controller preserves material byte-for-byte; configuration changes
produce a new revision without changing material; omitted management/shared
services produce a broker-only package without a payout wallet; EU transcode
and US audio/LLM configurations pass; changed package content fails validation.
Unit tests also cover missing/replaced identity refusal, input path escapes,
material replacement refusal and recovery after password/token-only writes.

All provisioning/parser acceptance containers ran with networking disabled and
synthetic RPC endpoints and image locks. They had no Docker socket. Those image
locks are test references, not evidence that corresponding deployable images
exist. Parser validation uses the setup image's embedded module binaries; it
cannot attest that an arbitrary operator image lock matches those binaries.

The artifact export `/tmp/livepeer-pool-setup-local.tar.gz` contains only the
setup image. `/tmp/open-pool-container-setup.tar.gz` contains operator Compose,
documentation and a public-input `.env` for the existing US node. Neither archive
contains generated wallet material, RPC credentials or service tokens.

Remote `us-central` generation/validation, actual ingress, receiver authorization,
funding, GPU readiness/certification and paid-work/payout rollout remain separate
operator steps. This evidence does not close the regional epic's live gates.
