# Dedicated regional payout wallets

Configure each executor with its controller's immutable `pool_controller.pool_id`,
scoped HTTPS `token_file`, and `executor.expected_wallet_address`. A regional
executor requires a persistent `executor.state_path`; keep both that run-history
file and the separate `executor.intent_store_path` (default `payout-intents.db`
next to it) on the region's persistent volume.

Before transaction recovery starts, the executor verifies the loaded key matches
the expected address and binds the transaction store to that address, chain and
pool. Reusing the store with another pool, wallet or chain fails before signing.
A populated legacy transaction store cannot be assigned a guessed regional
identity. Preserve it for explicit migration review. Do not delete a store to
bypass a failed identity check: doing so loses pending transaction evidence.

EU and US must use different payout wallets, executors, keys and transaction
stores. Nonce coordination is local to one executor. Provision an encrypted
keystore and password secret independently for each region and verify its public
address before enabling payout execution. Keep the expected address in reviewed
configuration; do not derive it silently from whichever key happens to mount.

The operator funds each wallet with the member obligations plus native ETH for
gas. Member transaction value is the full approved amount. Insufficient balance
holds that region's execution; there is no automatic treasury or inter-region
transfer. Review the public wallet address, chain, pending nonces and funding
balance before an operator-managed top-up. This documentation performs no wallet
provisioning or funding.

Normal restart resumes the original durable transaction intents and nonce/hash
history. A repeated controller intent cannot change its destination or value.
Adoption of a regional submitted transaction requires RPC evidence that it was
signed by this wallet on this chain, sends the exact amount to the member, and
has no contract calldata. An unknown transaction plus a nonce hint is insufficient.
Back up the region's controller and both executor databases with its configuration
and protected keys; stop writers or take coherent database snapshots. Restore
those same identities and files together, then inspect pending transactions before
resuming. Never start two executors from copies of the same wallet/store.
