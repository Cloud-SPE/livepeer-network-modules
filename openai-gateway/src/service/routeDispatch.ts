import type { RouteCandidate, RouteSelector } from "./routeSelector.js";
import { LivepeerBrokerError } from "../livepeer/errors.js";
import * as httpMultipart from "../livepeer/http-multipart.js";
import * as httpReqresp from "../livepeer/http-reqresp.js";
import * as httpStream from "../livepeer/http-stream.js";
import { buildPayment } from "../livepeer/payment.js";

interface DispatchCommon {
  routeSelector: RouteSelector;
  capability: string;
  offering: string;
  interactionMode?: string;
  requestId: string;
  request: import("fastify").FastifyRequest;
}

interface ReqRespDispatch extends DispatchCommon {
  body: BodyInit | null;
  contentType?: string;
}

interface MultipartDispatch extends DispatchCommon {
  body: FormData | Buffer | string;
  contentType?: string;
}

interface StreamDispatch extends DispatchCommon {
  body: string | Buffer | null;
  contentType?: string;
}

export async function dispatchReqresp(opts: ReqRespDispatch): Promise<httpReqresp.SendResult> {
  return attemptCandidates(
    opts.routeSelector,
    { capability: opts.capability, offering: opts.offering, interactionMode: opts.interactionMode, request: opts.request },
    async (candidate) =>
      httpReqresp.send({
        brokerUrl: candidate.brokerUrl,
        capability: opts.capability,
        offering: candidate.offering,
        paymentBlob: await buildPayment({
          capabilityId: opts.capability,
          offeringId: candidate.offering,
          recipientHex: candidate.ethAddress,
          brokerUrl: candidate.brokerUrl,
        }),
        body: opts.body,
        contentType: opts.contentType,
        requestId: opts.requestId,
      }),
  );
}

export async function dispatchMultipart(opts: MultipartDispatch): Promise<httpMultipart.SendResult> {
  return attemptCandidates(
    opts.routeSelector,
    { capability: opts.capability, offering: opts.offering, interactionMode: opts.interactionMode, request: opts.request },
    async (candidate) =>
      httpMultipart.send({
        brokerUrl: candidate.brokerUrl,
        capability: opts.capability,
        offering: candidate.offering,
        paymentBlob: await buildPayment({
          capabilityId: opts.capability,
          offeringId: candidate.offering,
          recipientHex: candidate.ethAddress,
          brokerUrl: candidate.brokerUrl,
        }),
        body: opts.body,
        contentType: opts.contentType,
        requestId: opts.requestId,
      }),
  );
}

export async function dispatchStream(opts: StreamDispatch): Promise<httpStream.StreamHandle> {
  return attemptCandidates(
    opts.routeSelector,
    { capability: opts.capability, offering: opts.offering, interactionMode: opts.interactionMode, request: opts.request },
    async (candidate) =>
      httpStream.sendStreaming({
        brokerUrl: candidate.brokerUrl,
        capability: opts.capability,
        offering: candidate.offering,
        paymentBlob: await buildPayment({
          capabilityId: opts.capability,
          offeringId: candidate.offering,
          recipientHex: candidate.ethAddress,
          brokerUrl: candidate.brokerUrl,
        }),
        body: opts.body,
        contentType: opts.contentType,
        requestId: opts.requestId,
      }),
  );
}

export async function selectRealtimeCandidate(
  routeSelector: RouteSelector,
  request: import("fastify").FastifyRequest,
  capability: string,
  offering: string,
  interactionMode?: string,
): Promise<RouteCandidate> {
  const candidates = await routeSelector.select({ capability, offering, interactionMode, request });
  if (candidates.length === 0) {
    throw new Error(`no route candidates for capability=${capability} offering=${offering}`);
  }
  return candidates[0]!;
}

async function attemptCandidates<T>(
  routeSelector: RouteSelector,
  input: { capability: string; offering: string; interactionMode?: string; request: import("fastify").FastifyRequest },
  fn: (candidate: RouteCandidate) => Promise<T>,
): Promise<T> {
  const candidates = await routeSelector.select(input);
  if (candidates.length === 0) {
    throw new Error(`no route candidates for capability=${input.capability} offering=${input.offering}`);
  }

  let lastError: unknown = null;
  let lastQuoteRejection: Error | null = null;
  for (const candidate of candidates) {
    try {
      const result = await fn(candidate);
      routeSelector.recordOutcome(candidate, { ok: true, retryable: false });
      return result;
    } catch (err) {
      lastError = err;
      if (isQuoteRejection(err)) {
        lastQuoteRejection = err instanceof Error ? err : new Error(String(err));
      }
      routeSelector.recordOutcome(candidate, { ok: false, retryable: shouldPenalize(err) }, describeFailure(err));
      if (!shouldRetry(err)) break;
    }
  }

  if (lastQuoteRejection !== null) {
    throw new LivepeerBrokerError({
      status: 503,
      code: "broker_quote_rejected",
      message: lastQuoteRejection.message,
    });
  }
  throw lastError;
}

// isQuoteRejection identifies dispatch failures that mean the route was
// selected but its quote/fingerprint contract was rejected — either by
// the payer-daemon's sender validation (`quote_ref.constraint_fingerprint
// is empty`, `quote_ref.route_fingerprint is empty`, `quote_ref.quote_id
// is empty`) or by a resolver that gates SelectMany on the same fields
// (`resolver selected route missing quote fingerprints`). These are
// distinct from "no route candidates" and should surface to the customer
// as a separate error class so dashboards can say "the broker can't
// quote this model" instead of "no route".
export function isQuoteRejection(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : typeof err === "string" ? err : "";
  if (!msg) return false;
  return (
    msg.includes("quote_ref") ||
    msg.includes("missing quote fingerprints") ||
    msg.includes("constraint_fingerprint") ||
    msg.includes("route_fingerprint")
  );
}

function shouldRetry(err: unknown): boolean {
  if (!(err instanceof LivepeerBrokerError)) return true;
  return err.status >= 500;
}

function shouldPenalize(err: unknown): boolean {
  if (!(err instanceof LivepeerBrokerError)) return true;
  return err.status >= 500 || err.status === 429;
}

function describeFailure(err: unknown): string {
  if (err instanceof LivepeerBrokerError) {
    return `${err.code}:${err.status}`;
  }
  if (err instanceof Error && err.message) {
    return err.message;
  }
  return "unknown_failure";
}
