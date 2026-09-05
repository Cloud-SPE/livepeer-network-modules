# Cleanup packet — `livepeer-modules-open-clearinghouse`

Date: 2026-09-05. For the LOC team. Based on a read of the repository at
its 2026-09-05 head against every change landed in
`livepeer-network-modules` between 2026-09-01 and 2026-09-05 (plans
0045–0048).

**Nothing here is blocking.** The clearinghouse sits on surfaces this
work did not touch — job and session minting, settlement verification,
offerings read through the registry daemon — and the sweep found no
breakage: capability and offering ids are opaque strings in LOC, the
descriptor-tag pattern matches ours, and `_bill()` takes `per_units` from
the pinned route, so the templates now priced per 1,000 units settle
correctly. Two things are stale, and one of them weakens a guarantee you
rely on.

## 1. Two session-axis values no broker accepts any more

`offering-axes.md` 1.0.8 (2026-09-02) removed `attachment: inband-ws` and
`metering: broker-observed`. Both had been accepted by brokers and served
by none — the broker's session WebSocket is the paid-session §8 control
socket, not a media relay — and every session data plane is now
`external` with `runner-reported` metering. Each axis has one value.

LOC still lists both:

- `src/livepeer_open_clearinghouse/providers/registry_daemon/client.py:90–101`
  and `domains/sessions/types.py:19–21` — `SessionAxes` Literals, plus the
  validator "broker-observed metering requires attachment='inband-ws'".
- `sdks/typescript/src/client.ts:76–80` — the `SessionAxes` interface.

A manifest carrying `external` / `runner-reported` validates today, so
nothing fails. But an SDK caller who picks `inband-ws` from the type gets
a broker refusal they could not have predicted from the SDK. Narrow both
to the single value and delete the pairing validator. Additive-tolerant
parsing (`extra="allow"`) is right and should stay.

## 2. The conformance mock broker mirrors a broker that no longer exists

`conformance/mock_broker` and the scenarios under `conformance/scenarios`
still model:

- `POST /v1/cap` (removed with plan 0043; the broker serves `POST /v1/job`
  and `/v1/session/*`) — `conformance/README.md:57`;
- descriptor tags like `livepeer.session.test/v1`, which LOC's own
  `descriptor_schema` pattern (`^[a-z][a-z0-9-]*/v[0-9]+$`) rejects —
  `case_d_bounded_session.json:15`, `case_d_extensible_session.json:15`;
- `attachment: "direct"`, `metering: "broker"` — values that never existed
  in offering-axes — `sdks/typescript/tests/client.test.ts:393–397`.

The SDK *source* is current (`client.ts:328` posts `/v1/job`;
`client.test.ts:310` asserts `Livepeer-Mode` is absent). The mock is not,
and since the harness proves SDK conformance by running against the
mock, it currently proves conformance to a wire nobody serves. Bring the
mock and scenarios to the shapes in `protocols/paid-job.md`,
`protocols/paid-session.md` and a real tag (`sfu-room/v1`, or the test
tag written as `livepeer-session-test/v1`). Better: point one runner at
`livepeer-network-protocol/conformance --serve-runner`, which attaches
the protocol suite's own fake runner to a *real* broker and stays up —
then the SDK runs against the broker the network actually ships.

## 3. Not an issue, for the record

- `_SAMPLE_ROUTES` (`registry_daemon/client.py:388`) carries
  `livepeer:transcoder/h264` — sample data, not a contract. The
  vocabulary rule (runner-attach §3.2) would spell it
  `video:transcode.vod`; change it if the sample is ever shown to users.
- The transcode work unit is now aggregate frame-megapixels claimed in
  the stream's `Livepeer-Work-Units` trailer. Your verification reads the
  claim from the signed settlement record and computes the bill from the
  pinned `per_units`; the trailer path is the same path it has always
  been. No change.
- No hard-coded capability ids, no `Livepeer-Mode` in SDK source, no
  dependency on the Go protocol modules.

## What we need from you

Nothing that blocks anything. A note when §1 and §2 land, so the
verification record for this sweep can close.
