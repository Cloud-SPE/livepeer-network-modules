# YubiHSM 2 setup — stub

Lands in commit 6 of plan 0019. This file is a placeholder so the
documentation index is stable from commit 2 onward.

The walkthrough will cover:

- `yubihsm-shell` install on the secure-orch host (Linux distro
  packages or Yubico's source build).
- Connector daemon configuration — `127.0.0.1`-bound only.
- secp256k1 key generation on-device (capability flags,
  domain assignment).
- PIN setup + audit-log enabling.
- Backup + recovery — wrap key, share-of-secret restore.
- Selector flag wiring: `--keystore=yubihsm:<connector-url>` in place
  of the default `--keystore=v3:<path>`.

YubiHSM 2 hardware is operator-supplied (~$650) and bus-attached
(USB-A). Network-attached HSM offerings (Thales Luna, AWS CloudHSM,
Entrust nShield Connect) are explicitly **not** supported — they
require network reachability and violate the hard rule.
