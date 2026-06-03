---
title: payment-daemon — payout modeling guide
status: accepted
last-reviewed: 2026-06-03
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

## Quick workflow: setting prices with the simulator

Use the simulator as a pricing worksheet. The workflow is:

1. Pick a redeemable ticket face value.
2. Pick the target expected payout cadence.
3. Enter expected workload volume.
4. Enter measured hardware throughput and all-in hourly cost.
5. Run the simulator.
6. Set the receiver price to the larger of:
   - payout-cadence floor
   - hardware break-even floor
7. Set the sender max ticket face value and funded-value guard to match
   the policy.

For the current OpenAI pricing scenario, the shared policy is:

```yaml
payout_policy:
  target_face_value_wei: "5400000000000000"    # 0.0054 ETH
  sender_max_face_value_wei: "5400000000000000"
  target_hours_per_win: 24
  gas_price_wei: "40000000"
  redeem_gas: 500000
```

With `ETH/USD = 1990`, that means every offering needs to generate:

```text
5,400,000,000,000,000 wei / 24h
= 225,000,000,000,000 wei/hour
~= $0.44775/hour
```

The receiver publishes `price.amount_wei` and `price.per_units`.
The sender uses those same prices to compute how much expected value it
is willing to fund. The sender must reject receiver ticket params when:

```text
requested_ticket_ev_sum > sender_funded_value
```

For the current scenario, cadence break-even prices are:

| Offering | `amount_wei` | `per_units` | Unit price |
|---|---:|---:|---:|
| `qwen3.6-35b-a3b-fp8-default` | `300000000000` | `1000` | `300000000 wei/token` |
| `vllm-qwen3-coder-30b-stream` | `600000000000` | `1000` | `600000000 wei/token` |
| `vllm-qwen3.6-27b-stream` | `696774194000` | `1000` | `696774194 wei/token` |
| `qwen3-8b-chat` | `300000000000` | `1000` | `300000000 wei/token` |
| `FLUX.1-dev` | `18750000000000` | `1` | `18750000000000 wei/image` |
| `kokoro` | `937500000000` | `1` | `937500000000 wei/request` |
| `video-abr-default` | `18750000000000` | `1` | `18750000000000 wei/job` |

These are not guaranteed profitable prices for every GPU. They only hit
the selected payout cadence at the configured volume. If the hardware
table shows negative profit for a GPU, raise price, raise volume, lower
cost, or use that GPU for a different offering.

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

### Practical target: `0.0054 ETH`

For current receiver-side redemption assumptions, a useful target
winning payout is:

```text
F = 0.0054 ETH = 5,400,000,000,000,000 wei
```

With:

```text
gas_price = 40,000,000 wei
redeem_gas = 500,000
redemption_cost = 20,000,000,000,000 wei
```

that gives:

```text
face_value / redemption_cost = 270x
```

This is large enough that the receiver is not trying to redeem dust, but
it is not the sender's spend limit. The sender's spend limit is expected
value.

## 7. Sender and receiver must agree on EV, not just face value

The receiver can ask for ticket parameters, but the sender must enforce
what it is willing to pay before signing.

For a request:

```text
request_ev = units * price_per_unit
```

For a ticket:

```text
ticket_ev = face_value * win_prob / 2^256
```

The invariant is:

```text
sum(ticket_ev for request) <= sender_funded_value_for_request
```

That is the guardrail that prevents the receiver from extracting more
value than the sender agreed to send. A high face value is fine if the
win probability is lowered so EV stays within the sender's funded value.

Example:

```text
request_ev = 251,150,000,000 wei
face_value = 5,400,000,000,000,000 wei
win odds ~= 1 in 21,500
```

The winning payout is much larger than the request value, but the
expected value is still about the request value. This is the intended
probabilistic-payment shape.

## 8. Hardware profitability mental model

The simulator has three layers. Keep them separate.

### Layer A: sender spend

The sender decides the most expected value it is willing to fund:

```text
funded_value = estimated_units * price_per_unit
```

The sender should reject ticket params where:

```text
ticket_ev_sum > funded_value
```

This protects gateways from a receiver asking for more value than the
gateway intended to send.

### Layer B: receiver payout cadence

The receiver cares about when redeemable wins arrive:

```text
expected_wins_per_hour = revenue_per_hour / face_value
expected_hours_per_win = face_value / revenue_per_hour
```

For planning windows:

```text
expected_wins_1d = expected_wins_per_hour * 24
expected_wins_7d = expected_wins_per_hour * 168
```

Short windows are noisy. `expected_wins_1d = 0.5` does not mean half a
ticket arrives. It means the long-run expectation is one win every two
days, and many one-day windows will have zero redemptions.

### Layer C: hardware economics

The orchestrator decides whether the workload pays for the GPU:

```text
revenue_per_hour = measured_units_per_hour * price_per_unit
profit_per_hour = revenue_per_hour - all_in_hardware_cost_per_hour
```

Use all-in hourly cost:

- cloud rental or amortized capex
- power
- host CPU / RAM / disk
- network
- operator margin or overhead

The key hardware questions are:

- `expected_payout_1d >= hardware_cost_1d`
- `expected_payout_7d >= hardware_cost_7d`
- `expected_wins_7d` is high enough that variance is tolerable

If expected profit is positive but `expected_wins_7d` is near zero, the
price may be economically fair in expectation but operationally too
lumpy. Fix that with more aggregation, a lower face value for that lane,
or higher traffic volume.

### What operators should enter

For each offering and GPU class, operators should enter:

```yaml
hardware:
  - name: "RTX 4090"
    units_per_hour: 650000
    hourly_cost_usd: 0.55
```

`units_per_hour` must be measured for the exact model and serving mode.
A `4090` running a small batch-friendly image model and a `4090` running
a long-context chat model are different economic objects.

### Break-even pricing rule

For a receiver, the price should clear both hardware cost and payout
cadence.

For chat, first convert raw token traffic into billable units. Input and
output tokens do not have to cost the same. A practical starting point is:

```text
billable_tokens_per_day =
  input_tokens_per_day + output_tokens_per_day * output_weight
```

Use `output_weight = 1` only if input and output are equally expensive.
For hosted LLM inference, output is usually more expensive because it
consumes decode time, so a weight such as `2` or `3` is often a clearer
starting point than pretending all tokens are identical.

Example simulator input:

```yaml
work_unit: "tokens"
workload:
  requests_per_hour: 360
  token_mix:
    input_tokens_per_day: 6000000
    output_tokens_per_day: 4000000
    output_weight: 3
```

The simulator derives:

```text
billable_tokens_per_day = 6,000,000 + 4,000,000 * 3
                        = 18,000,000
billable_tokens_per_hour = 750,000
billable_tokens_per_request = 750,000 / 360 ~= 2,083
```

Hardware break-even:

```text
hardware_floor_per_unit = hardware_cost_per_hour / measured_units_per_hour
```

Payout-cadence break-even:

```text
cadence_floor_per_unit = face_value / (target_hours_per_win * expected_units_per_hour)
```

Use the larger value:

```text
price_per_unit = max(hardware_floor_per_unit, cadence_floor_per_unit)
```

If the orchestrator does not want markup yet, stop there. That is
break-even. Later, add margin:

```text
price_per_unit = break_even_price_per_unit * (1 + margin_pct)
```

Volume improves margin when the hardware cost is mostly fixed. If the
same GPU serves more units per hour, then:

```text
hardware_cost_per_unit = hardware_cost_per_hour / units_per_hour
```

falls as volume rises. If the published price stays constant, the
operator margin per unit rises. If the market requires lower prices at
higher volume, the simulator should be used to find the new break-even.

Example with `ETH/USD = 1990`, `face_value = 0.0054 ETH`, and
`target_hours_per_win = 24`:

```text
required_revenue_per_hour = 5,400,000,000,000,000 wei / 24
                          = 225,000,000,000,000 wei/hour
                          ~= $0.44775/hour
```

For `FLUX` at `12 images/hour`:

```text
price_per_image = 225,000,000,000,000 / 12
                = 18,750,000,000,000 wei/image
                ~= $0.0373/image
```

For `Kokoro` at `240 requests/hour`:

```text
price_per_request = 225,000,000,000,000 / 240
                  = 937,500,000,000 wei/request
                  ~= $0.00187/request
```

Those are not markup prices. They are break-even prices for the chosen
ticket face value, expected volume, and 24-hour payout-cadence target.

## 9. The recommended conversation structure

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

## 10. Worked examples

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

## 11. What this model does and does not prove

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

## 12. Simulation before chain integration

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
- winning ticket face value
- expected value per request and per ticket
- win probability and approximate odds
- expected wins per hour
- expected time between wins
- gateway estimated units and funded value guidance
- gateway funding / request EV ratio
- estimated redemption cost and face-value margin
- optional hardware profitability tables:
  - expected payout over 1 day and 7 days
  - hardware cost over 1 day and 7 days
  - expected profit over 1 day and 7 days
  - expected wins over 1 day and 7 days
  - break-even units/hour for each GPU profile
- Monte Carlo p50 / p90 / p99 wait time between wins
- a starter `host-config.yaml` snippet

### How to turn simulator output into settings

Use these report fields as the decision surface:

| Report field | Meaning | Action |
|---|---|---|
| `Target price floor` | minimum unit price for the configured payout cadence | receiver should not price below this unless it accepts slower payouts |
| `amount_wei` / `per_units` in the snippet | concrete price config derived from the scenario | copy to receiver offering config and sender price expectations |
| `Configured revenue/hour` | expected value generated by the configured workload and price | compare to target revenue/hour and hardware cost/hour |
| `Fairness ratio` | configured price divided by target floor | `1.00x` hits cadence target; below `1.00x` is underpriced for that target |
| `Expected hours/win` | average wait for a redeemable winning ticket | use for operator payout expectations |
| `Volume multiplier for target cadence` | how much more volume or price is needed | values above `1.00x` mean the offering misses the cadence target |
| `Gateway funding / request EV` | sender-funded value divided by requested ticket EV | must be `>= 1.00x`; otherwise the sender should reject or top up |
| hardware `Profit/hr` | GPU economics at the configured unit price | must be non-negative for the target hardware unless break-even is intentionally deferred |
| hardware `Break-even units/hr` | required throughput for that GPU at the current price | if measured throughput is lower, raise price or do not route that workload there |

The receiver-side price-setting rule is:

```text
cadence_price = face_value / (target_hours_per_win * billable_units_per_hour)
hardware_price = hardware_cost_per_hour / measured_units_per_hour
receiver_price = max(cadence_price, hardware_price)
```

For YAML prices:

```text
amount_wei = receiver_price * per_units
```

Example for chat:

```text
receiver_price = 300,000,000 wei/token
per_units = 1000
amount_wei = 300,000,000,000
```

Example receiver offering config:

```yaml
price:
  amount_wei: "300000000000"
  per_units: 1000
```

The sender should use the same unit price to decide how much value it is
willing to fund, but it should independently enforce:

```text
sender_max_face_value_wei >= requested_face_value_wei
funded_value_wei >= requested_ticket_ev_sum
```

This keeps both sides equitable:

- receiver gets a price that matches its cost and payout-cadence target
- sender never signs tickets worth more expected value than it intended
  to pay

Use it to compare:

- orch-side fairness:
  - "is this published wholesale price above the operator's minimum
    sustainable floor?"
- gateway-side spend:
  - "if I use this price and this estimated-unit policy, what budget do
    I mint per request or per session top-up?"
- receiver-side redeemability:
  - "is the winning face value comfortably above gas?"
- sender-side safety:
  - "does the requested ticket EV fit inside the funded value I intended
    to send?"
- receiver-side hardware economics:
  - "does this model and price pay for a 3090 / 4090 / A100 / H100 style
    deployment over 1-day and 7-day windows?"

### Multi-offering ticket-sizing scenario

Use the multi-offering scenario when explaining why different job types
can share a winning payout but have different payout cadence:

```sh
go run ./cmd/payout-sim \
  --scenario ./scenarios/openai-multi-offering-ticket-sizing.yaml \
  --format markdown \
  --chart-out ./scenarios/openai-multi-offering-ticket-sizing.svg
```

That scenario uses:

```yaml
payout_policy:
  target_face_value_wei: "5400000000000000"
  sender_max_face_value_wei: "5400000000000000"
  target_hours_per_win: 24
  gas_price_wei: "40000000"
  redeem_gas: 500000
```

The chart is a communication artifact, not a protocol dependency. It is
useful in reviews because it shows:

- one shared redeemable winning payout
- the target payout cadence for that face value
- how much more volume or revenue each offering needs to hit that target
- which workloads are not economically viable under the configured price
  and face value

## 13. Why we still need the real integration

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

## 14. Suggested operator summary

If you need a short explanation for a design review or operator call,
use this:

- expected earnings come from `work_units * price_per_unit`
- payout timing comes from `face_value / earnings_rate`
- lowering price slows payout cadence
- increasing workload speeds payout cadence
- increasing face value makes payout lumpier but keeps redemption
  economic
- sender-side safety comes from validating ticket EV against the sender's
  funded value before signing
- low-volume orchs are variance-dominated even when pricing is correct

That is the right mental model for payout planning in this system.
