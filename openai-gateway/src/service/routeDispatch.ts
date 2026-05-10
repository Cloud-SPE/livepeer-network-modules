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
  for (const candidate of candidates) {
    try {
      return await fn(candidate);
    } catch (err) {
      lastError = err;
      if (!shouldRetry(err)) break;
    }
  }

  throw lastError;
}

function shouldRetry(err: unknown): boolean {
  if (!(err instanceof LivepeerBrokerError)) return true;
  return err.status >= 500;
}
