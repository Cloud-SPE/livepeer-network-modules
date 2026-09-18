# Shared member interface

The server renders membership, host status and reporting pages as semantic HTML.
Wallet connection and explicit form submission use a small external script;
styles live in the checked-in stylesheet. Every member action uses the session
wallet, selected pool and a controller-validated member token.

A wallet signs in once, then accepts the displayed regional version to join EU,
US or both. Reading a region never joins it. Updated terms require a new
acceptance. The interface shows controller availability and configured workload
descriptions without exposing member data. An unavailable membership is unknown,
not unjoined. A missing accounting response is unavailable, not a zero balance.

Enrollment and credential rotation prepare a one-use, ten-minute bundle link.
The portal verifies the returned pool/wallet/enrollment and fetches the ZIP from
the configured controller using its one-time enrollment credential. It never
follows a returned bundle URL or HTTP redirect. Links are high-entropy bearer
secrets: avoid sharing them or recording full install/download paths in proxy
access logs. The persistent portal store contains protected temporary bundles;
include it in sensitive-state backup handling and never expose that directory.

The installer requires Docker Compose, curl, unzip and the host's GPU runtime.
It uses an enrollment-specific directory under
`~/.local/share/livepeer-pool/<enrollment_id>`. A selected-GPU bundle leaves
unselected devices to their existing enrollment. Rotation reinstalls the same
enrollment's configuration. Download and inspect the bundle instead of using
the one-command installer when desired. Never execute an install command in the
agent's development workspace as part of a test.

The portal shows each host's GPUs, placements, reason codes, evidence and latest
reported agent apply result. The controller persists apply revision, time and
service details across restart. These are read-only observations for members;
only the agent enrollment credential may submit apply or stop evidence.

Members may decline workload templates, remove a prior opt-out, rotate their
credential, retire an enrollment, or request a selected GPU's regional transfer.
Transfers expose pending progress and reasons. The source must finish the
accepted drain/revoke/actual-stop protocol before release. Once released, the
member joins the destination and enrolls the selected GPU UUID there. The UI
provides no ownership recovery action or timeout override.

`make test-browser` runs the real portal, wallet challenge/signature/session
flow, pinned regional client and browser interface against controlled regional
service fixtures. It records desktop/mobile screenshots, browser checks and a
Go race/coverage profile in a temporary evidence directory. It does not execute
the installer, transact on-chain or represent a live regional rollout. The
cross-component acceptance suite validates the independent regional services.

When replacing an existing enrollment, the installer downloads the bundle before
stopping that enrollment's agent, then replaces its credential files and restarts
it. This prevents the old agent's automatic rotation from overwriting a new
manual credential bundle. Agent credential recovery is documented in
[regional credentials](../../pool-member-agent/docs/regional-credentials.md).
