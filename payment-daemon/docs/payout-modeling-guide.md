---
title: payment-daemon — payout modeling guide
status: accepted
last-reviewed: 2026-05-23
audience: orchestrator operators, gateway operators, pricing / economics work
---

# Payout modeling guide

This guide gives operators a practical way to talk about probabilistic
micropayments without collapsing into the wrong question.

The wrong question is:

- "Will this exact request win a ticket?"

The right questions are:

- "What payout rate does this workload produce?"
- "How often should an orch expect a redeemable win?"
- "How does payout cadence change if workload or unit price changes?"

The ticket system is probabilistic. A single ticket is not promised to
win ahead of time. What the system preserves is expected value over many
tickets and many requests.

## 1. The four numbers that matter

Use these symbols in conversation:

| Symbol | Meaning |
|---|---|
| `u` | work units per request, or work units per second |
| `p` | price per work unit |
| `r` | value generation rate, where `r = u * p` |
| `F` | winning ticket face value |

From those, derive the rest.

## 2. Expected payout vs payout cadence

The expected value of a ticket is:

```text
EV = face_value * win_prob / 2^256
```

At the workload level:

```text
expected payout = work_units * price_per_unit
```

Over time, these converge:

```text
total expected payout ~= total paid work value
```

The operator-facing distinction is:

- `price_per_unit` controls how much value the orch earns
- `face_value` controls how lumpy or smooth payout arrival feels
- `win_prob` adjusts so expected value stays honest

That means the expected payout slope and the payout timing are different
conversations.

## 3. The payout-latency model

Treat payout timing as a cadence problem.

If:

```text
r = u * p
```

then:

```text
win probability per ticket          w ~= r / F
expected tickets per win            ~= F / r
expected work units per win         ~= F / p
expected time until one win         ~= F / r
expected wins per unit time         ~= r / F
```

This is the most useful operational framing.

### Read it in plain language

- Higher `price_per_unit` means faster payout cadence.
- Higher workload volume means faster payout cadence.
- Higher `face_value` means slower payout cadence, but bigger wins.
- Lower `face_value` means faster payout cadence, but more pressure from
  gas economics.

## 4. What changes when price changes

Holding workload rate and ticket face value constant:

- if `price_per_unit` doubles, expected time between wins is cut roughly
  in half
- if `price_per_unit` halves, expected time between wins roughly doubles

So if an orch asks:

- "If I charge less per work unit, how much slower do I get paid?"

the answer is:

- payout frequency scales roughly inversely with unit price

Example:

- face value `F = $5`
- workload rate = `$1/hour`
- expected wins = `1 every 5 hours`

If the same orch cuts price in half and now earns `$0.50/hour`:

- expected wins = `1 every 10 hours`

If the orch doubles price and still sustains the same workload volume:

- expected wins = `1 every 2.5 hours`

## 5. What changes when workload changes

Holding price and face value constant:

- if work rate doubles, expected time between wins is cut roughly in
  half
- if work rate halves, expected time between wins roughly doubles

This is the cleanest way to explain payout behavior for busy vs quiet
orchestrators:

- high-volume orchs see frequent redemptions and lower variance
- low-volume orchs can have correct long-run economics but still wait a
  long time for a win

## 6. Why face value cannot be arbitrarily small

`face_value` is not just a UX knob. It is bounded by redemption
economics.

If `face_value` is too small relative to:

- redemption gas
- gas price
- desired operator margin

then winning tickets become uneconomic to redeem.

This is why:

- very cheap work often implies low `win_prob`, not tiny `face_value`
- per-request payment intuition is misleading for low-value workloads
- session funding or batched runway is usually the right model for long
  or low-priced workloads

## 7. The recommended conversation structure

When discussing payout behavior with operators, use this sequence.

### Step 1: choose the work metric

Examples:

- tokens
- images
- seconds
- media segments
- requests

### Step 2: estimate workload rate

Examples:

- `200 tokens/sec`
- `30 images/hour`
- `8 minutes of media/hour`

### Step 3: set retail price per unit

Examples:

- `2e12 wei/token`
- `5e15 wei/image`

### Step 4: compute value-generation rate

```text
r = workload_rate * price_per_unit
```

This is the orch's expected earnings slope.

### Step 5: choose a redeemable face value

Use a face value that is large enough to clear gas cost with margin.
This is chain- and market-dependent, but the principle is stable:

- payout granularity should sit comfortably above redemption cost

### Step 6: compute payout cadence

```text
expected time per win = F / r
```

This gives the operator the practical answer they care about:

- "At this workload and price, how often should money actually hit the
  chain?"

## 8. Worked examples

### Example A: busy orch

Inputs:

- workload rate = `500 tokens/sec`
- price = `2e12 wei/token`
- face value = `5e16 wei`

Derived:

```text
r = 500 * 2e12 = 1e15 wei/sec
expected time per win = 5e16 / 1e15 = 50 sec
```

Interpretation:

- the orch earns `1e15 wei/sec` in expectation
- a redeemable win should arrive about every 50 seconds on average

### Example B: quiet orch

Inputs:

- workload rate = `20 tokens/sec`
- price = `2e12 wei/token`
- face value = `5e16 wei`

Derived:

```text
r = 20 * 2e12 = 4e13 wei/sec
expected time per win = 5e16 / 4e13 = 1250 sec
```

Interpretation:

- same unit price
- much lower workload volume
- payout cadence slows to about one win every 21 minutes

### Example C: price cut

Start from Example B, then halve `price`:

```text
r = 20 * 1e12 = 2e13 wei/sec
expected time per win = 5e16 / 2e13 = 2500 sec
```

Interpretation:

- halving price roughly doubled expected wait time between wins

## 9. What this model does and does not prove

This model is good for:

- choosing a retail unit price
- explaining payout smoothness vs lumpiness
- estimating expected wins per hour/day/week
- comparing high-volume vs low-volume orch behavior

This model does not prove:

- that a specific payment will win before it is sent
- that short windows will exactly match expectation
- that very low-volume orchs will see stable payout cadence

Variance matters. Low traffic means noisy outcomes.

## 10. Simulation before chain integration

Yes, a simulation is the right next step before relying on a real
integration harness for pricing decisions.

The simplest useful simulation does not need a chain at all. It only
needs:

- workload stream
- chosen `price_per_unit`
- chosen `face_value`
- derived `win_prob`
- repeated Bernoulli trials for ticket wins

For each simulated ticket:

1. compute funded expected value from `units * price_per_unit`
2. derive or accept `face_value`
3. derive `win_prob` from the EV target
4. sample whether the ticket wins
5. if it wins, add one redemption of size `face_value`
6. measure:
   - expected payout
   - realized payout
   - wins per hour/day
   - average and percentile wait time between wins
   - variance for low-volume workloads

That simulation should answer:

- how lumpy payout feels for a given orch volume
- how sensitive payout cadence is to a price change
- how much traffic is needed before observed revenue tracks expected
  revenue tightly

### Simulator command in this repo

This repo now carries a first-pass simulator:

- command: `payment-daemon/cmd/payout-sim`
- sample scenarios:
  - `payment-daemon/scenarios/openai-chat-qwen3-8b.yaml`
  - `payment-daemon/scenarios/video-abr-default.yaml`

Run it from `payment-daemon/`:

```sh
go run ./cmd/payout-sim --scenario ./scenarios/openai-chat-qwen3-8b.yaml --format text
go run ./cmd/payout-sim --scenario ./scenarios/video-abr-default.yaml --format markdown
```

Or via Make:

```sh
make sim SCENARIO=./scenarios/openai-chat-qwen3-8b.yaml
FORMAT=markdown make sim SCENARIO=./scenarios/video-abr-default.yaml
```

The simulator emits:

- configured revenue per hour
- target hourly-revenue price floor
- fairness ratio (`configured / floor`)
- expected wins per hour
- expected time between wins
- gateway estimated units and funded value guidance
- Monte Carlo p50 / p90 / p99 wait time between wins
- a starter `host-config.yaml` snippet

Use it to compare:

- orch-side fairness:
  - "is this published wholesale price above the operator's minimum
    sustainable floor?"
- gateway-side spend:
  - "if I use this price and this estimated-unit policy, what budget do
    I mint per request or per session top-up?"

## 11. Why we still need the real integration

Simulation is necessary but not sufficient.

A real integration harness is still needed to validate:

- sender ticket minting against real sender escrow / reserve
- receiver acceptance against real ticket params
- actual redemption submission and confirmation timing
- queue behavior under real gas conditions
- how chain confirmations stretch revenue-recognition latency beyond the
  raw "time to first winning ticket" model

Use simulation for economics exploration.
Use the integration harness for protocol truth.

## 12. Suggested operator summary

If you need a short explanation for a design review or operator call,
use this:

- expected earnings come from `work_units * price_per_unit`
- payout timing comes from `face_value / earnings_rate`
- lowering price slows payout cadence
- increasing workload speeds payout cadence
- increasing face value makes payout lumpier but keeps redemption
  economic
- low-volume orchs are variance-dominated even when pricing is correct

That is the right mental model for payout planning in this system.
