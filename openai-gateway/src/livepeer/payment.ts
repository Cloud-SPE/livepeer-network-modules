// Hand-rolled protobuf-3 encoder for the `Livepeer-Payment` envelope.
//
// The wire format is defined at
// `livepeer-network-protocol/proto/livepeer/payments/v1/payment.proto`:
//
//   message Payment {
//     string capability_id      = 1;
//     string offering_id        = 2;
//     uint64 expected_max_units = 3;
//     bytes  ticket             = 4;
//   }
//
// We encode without depending on protobufjs/google-protobuf so the
// openai-gateway image stays zero-runtime-deps. The field count is small
// and stable; if the schema grows, we'd revisit.
//
// v0.1: ticket bytes are an opaque stub. Real probabilistic-micropayment
// ticket generation is the chain-integration follow-up.

const STUB_TICKET = "openai-gateway-stub";
const DEFAULT_EXPECTED_MAX_UNITS = 10_000n;

export interface PaymentInputs {
  capabilityId: string;
  offeringId: string;
  expectedMaxUnits?: bigint;
  ticket?: string;
}

/**
 * Returns the base64-encoded `Livepeer-Payment` header value for the given
 * capability + offering pair.
 */
export function buildPayment(inputs: PaymentInputs): string {
  if (!inputs.capabilityId) {
    throw new Error("buildPayment: capabilityId is required");
  }
  if (!inputs.offeringId) {
    throw new Error("buildPayment: offeringId is required");
  }
  const max = inputs.expectedMaxUnits ?? DEFAULT_EXPECTED_MAX_UNITS;
  if (max <= 0n) {
    throw new Error("buildPayment: expectedMaxUnits must be > 0");
  }
  const ticket = inputs.ticket ?? STUB_TICKET;

  const out: number[] = [];
  writeStringField(out, 1, inputs.capabilityId);
  writeStringField(out, 2, inputs.offeringId);
  writeUint64Field(out, 3, max);
  writeBytesField(out, 4, encodeUtf8(ticket));

  return base64Encode(Uint8Array.from(out));
}

// Wire types per https://protobuf.dev/programming-guides/encoding/#structure
//   0 = varint (uint64)
//   2 = length-delimited (string, bytes)

function writeStringField(out: number[], fieldNum: number, value: string): void {
  if (value.length === 0) return; // proto3 omits default values; matches Go encoder
  const bytes = encodeUtf8(value);
  writeTag(out, fieldNum, 2);
  writeVarint(out, BigInt(bytes.length));
  for (const b of bytes) out.push(b);
}

function writeBytesField(out: number[], fieldNum: number, value: Uint8Array): void {
  if (value.length === 0) return;
  writeTag(out, fieldNum, 2);
  writeVarint(out, BigInt(value.length));
  for (const b of value) out.push(b);
}

function writeUint64Field(out: number[], fieldNum: number, value: bigint): void {
  if (value === 0n) return;
  writeTag(out, fieldNum, 0);
  writeVarint(out, value);
}

function writeTag(out: number[], fieldNum: number, wireType: number): void {
  writeVarint(out, BigInt((fieldNum << 3) | wireType));
}

function writeVarint(out: number[], value: bigint): void {
  let v = value;
  while (v > 0x7fn) {
    out.push(Number((v & 0x7fn) | 0x80n));
    v >>= 7n;
  }
  out.push(Number(v & 0x7fn));
}

function encodeUtf8(s: string): Uint8Array {
  return new TextEncoder().encode(s);
}

// Node's `Buffer.from(...).toString("base64")` would be simpler, but we
// avoid the Buffer dependency to keep this importable in browser-style
// builds too. The standard-encoding (with padding) matches the broker's
// `base64.StdEncoding.DecodeString`.
function base64Encode(bytes: Uint8Array): string {
  if (typeof Buffer !== "undefined") {
    return Buffer.from(bytes).toString("base64");
  }
  // Fallback for non-Node runtimes (browser, deno).
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  // btoa is part of Web APIs; in Node 16+ it's also a global.
  // eslint-disable-next-line no-undef
  return btoa(bin);
}
