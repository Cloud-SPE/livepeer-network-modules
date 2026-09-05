# integration-stack — a real broker + payment pair for downstream teams

A running `paid-job/v1` + `paid-session/v1` stack on a real chain, for
gateway and clearinghouse teams to integrate against.

It is deliberately **not** the onboarding scenario next door. That one is
shaped like a production orchestrator deployment (containers, ingress,
TLS). This one runs the binaries directly on one host so a failure is a
log line rather than an ingress mystery, and so the whole thing can be
restarted in a second while somebody is on a call about it.

## What it runs

| Process | Role |
|---|---|
| `payment-daemon --mode=sender` | the payer: mints signed ticket envelopes |
| `payment-daemon --mode=receiver` | the payee: validates, credits, debits, redeems |
| `capability-broker` | the paid surface — `/v1/job`, `/v1/session/*`, `/v1/exchange/*`, `/v1/settlement/*` |
| a stub backend | stands in for the runner that does the actual work |

Both payment daemons hold **real keys against a real chain**. A winning
ticket moves real value. The spend limits in `stack.env` are what bound
that, and they are the only thing that does.

## Run it

```bash
cp stack.env.example stack.env   # edit: keys, address, external URL
./up.sh                          # starts everything, waits for health
./status.sh                      # what is running, and on what
./down.sh                        # stops everything and removes sockets
```

`up.sh` is idempotent: it refuses to start a second copy rather than
racing the first for the same unix sockets.

## Why the scripts rather than a compose file

Because teardown has to be reliable. An earlier round of live testing
left two daemons running against mainnet for six hours after they were
believed stopped — the greps used to find them matched on a path that
the processes did not carry. `down.sh` tracks pids in a file written at
start, so stopping does not depend on pattern-matching a command line.

## Reaching it

`EXTERNAL_BASE_URL` is what the broker advertises to clients, so it has
to be an address they can actually reach — not `127.0.0.1` unless the
client is on this host.

The paid surface has no transport authentication of its own. Possession
of a session credential or a broker-minted id is the authorisation, by
design, so that a clearinghouse can read a settlement without holding a
gateway's credential. **Do not expose this to an untrusted network.**

## Integration guides

Per-team, in `docs/integration/`:

- `openai-gateway.md` — paid-job/v1, the funding ceiling, settlement retrieval
- `loc-clearinghouse.md` — settlement verification, encumbrance, evidence
- `meeting.md` — paid-session/v1, rotation, rebinding
- `vtuber-daydream.md` — paid-session/v1, trickle-egress/v1 and
  scope-passthrough/v1; workload identity lives in the descriptor schema

## Verifying the stack with the chain probe

`payment-daemon/cmd/livepeer-chain-probe` exercises both protocols
against this stack and a real ledger. All five modes pass against it.

```
go -C payment-daemon build -o /tmp/chain-probe ./cmd/livepeer-chain-probe

# paid-job — unary, and the one that checks the inline settlement headers
/tmp/chain-probe --recipient=$ORCH_ETH_ADDRESS --protocol=job \
  --capability=openai:chat-completions --offering=default \
  --work-unit=tokens --price-wei=100 --per-units=1000 \
  --payee-admin-token=$PAYEE_ADMIN_TOKEN

# paid-session and rotation — need the offering pointed at the probe's
# own runner, because the stub emits no usage events and so never meters
MEET_RUNNER_URL=http://127.0.0.1:9501 ./up.sh
/tmp/chain-probe --recipient=$ORCH_ETH_ADDRESS --protocol=session \
  --capability=meet:sfu-room --offering=default \
  --work-unit=participant-seconds --price-wei=100 --per-units=1000 \
  --runner-bind=127.0.0.1:9501 --payee-admin-token=$PAYEE_ADMIN_TOKEN
```

### The retry run

`--protocol=retry` needs the debit to fail from outside — a probe cannot
stop a daemon it did not start. Give the backend a stall window, then
kill the payee inside it and restart it on the **same `--db`**:

```
BACKEND_DELAY_SECONDS=25 ./up.sh
/tmp/chain-probe --protocol=retry ... &
sleep 4  && kill -9 $(sed -n 2p run/pids) && rm -f /tmp/lpm-payee.sock
sleep 22 && run/bin/payment-daemon --mode=receiver --db=run/payee.db ...
```

The same `--db` matters: the session has to survive the restart, and the
broker has to have left it open. A closed session refuses debits, so
closing one at end of exchange makes every retry fail no matter how
generous the budget.
