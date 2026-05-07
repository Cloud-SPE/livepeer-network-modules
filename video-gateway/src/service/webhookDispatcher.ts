import type { WebhookEvent } from "../engine/types/index.js";
import { signEvent, type SignedHeaders } from "../engine/service/webhookSigner.js";
import type {
  WebhookDeliveryRepo,
  WebhookEndpointRepo,
  WebhookFailureRepo,
} from "../repo/index.js";

export interface DispatchInput {
  endpointUrl: string;
  endpointSecret: string;
  event: WebhookEvent;
  deliveryId: string;
  pepper: string;
}

export interface DispatchResult {
  status: number;
  ok: boolean;
  body?: string;
}

export const DEFAULT_RETRY_BACKOFFS_SEC: readonly number[] = [1, 5, 30] as const;

export interface RetryDispatcherDeps {
  pepper: string;
  retryBackoffsSec?: readonly number[];
  fetchImpl?: typeof fetch;
  sleep?: (ms: number) => Promise<void>;
  now?: () => Date;
  newId?: () => string;
}

export interface RetryDispatcherInput {
  endpointId: string;
  endpointUrl: string;
  endpointSecret: string;
  deliveryId: string;
  event: WebhookEvent;
}

export interface RetryDispatcherOutcome {
  delivered: boolean;
  attempts: number;
  finalStatus: number | null;
  lastError: string | null;
  failureId?: string;
}

export interface RetryDispatcher {
  dispatch(input: RetryDispatcherInput): Promise<RetryDispatcherOutcome>;
  replayFailure(failureId: string): Promise<RetryDispatcherOutcome>;
}

export async function dispatchWebhook(input: DispatchInput): Promise<DispatchResult> {
  const body = JSON.stringify({
    type: input.event.type,
    occurred_at: input.event.occurredAt.toISOString(),
    data: input.event.data,
  });
  const signed = signEvent({
    secret: input.endpointSecret + input.pepper,
    body,
    eventType: input.event.type,
    deliveryId: input.deliveryId,
  });
  const headers: Record<string, string> = { ...signed };

  const res = await fetch(input.endpointUrl, {
    method: "POST",
    body,
    headers,
  });
  const result: DispatchResult = { status: res.status, ok: res.ok };
  try {
    result.body = await res.text();
  } catch {
    /* non-text body; skip */
  }
  return result;
}

function defaultSleep(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}

interface AttemptResult {
  status: number | null;
  ok: boolean;
  retryable: boolean;
  errorMessage: string | null;
}

async function attempt(
  url: string,
  body: string,
  headers: SignedHeaders,
  fetchImpl: typeof fetch,
): Promise<AttemptResult> {
  try {
    const res = await fetchImpl(url, {
      method: "POST",
      body,
      headers: { ...headers },
    });
    if (res.ok) return { status: res.status, ok: true, retryable: false, errorMessage: null };
    if (res.status >= 500) {
      return {
        status: res.status,
        ok: false,
        retryable: true,
        errorMessage: `webhook target returned ${res.status}`,
      };
    }
    return {
      status: res.status,
      ok: false,
      retryable: false,
      errorMessage: `webhook target returned ${res.status}`,
    };
  } catch (err) {
    return {
      status: null,
      ok: false,
      retryable: true,
      errorMessage: err instanceof Error ? err.message : String(err),
    };
  }
}

export function createRetryDispatcher(
  deps: RetryDispatcherDeps,
  repos: {
    endpoints: WebhookEndpointRepo;
    deliveries: WebhookDeliveryRepo;
    failures: WebhookFailureRepo;
  },
): RetryDispatcher {
  const fetchImpl = deps.fetchImpl ?? fetch;
  const sleep = deps.sleep ?? defaultSleep;
  const now = deps.now ?? (() => new Date());
  const newId = deps.newId ?? (() => `whf_${Math.random().toString(36).slice(2, 12)}`);
  const backoffs = deps.retryBackoffsSec ?? DEFAULT_RETRY_BACKOFFS_SEC;
  const totalAttempts = backoffs.length + 1;

  async function runWithRetry(
    url: string,
    body: string,
    headers: SignedHeaders,
  ): Promise<{ attempts: number; last: AttemptResult }> {
    let attempts = 0;
    let last: AttemptResult = {
      status: null,
      ok: false,
      retryable: true,
      errorMessage: "no attempts made",
    };
    for (let i = 0; i < totalAttempts; i++) {
      attempts = i + 1;
      last = await attempt(url, body, headers, fetchImpl);
      if (last.ok) return { attempts, last };
      if (!last.retryable) return { attempts, last };
      const remaining = totalAttempts - 1 - i;
      if (remaining > 0) {
        const waitSec = backoffs[i] ?? backoffs[backoffs.length - 1] ?? 1;
        await sleep(waitSec * 1000);
      }
    }
    return { attempts, last };
  }

  async function runDispatch(
    input: RetryDispatcherInput,
  ): Promise<RetryDispatcherOutcome> {
    const body = JSON.stringify({
      type: input.event.type,
      occurred_at: input.event.occurredAt.toISOString(),
      data: input.event.data,
    });
    const headers = signEvent({
      secret: input.endpointSecret + deps.pepper,
      body,
      eventType: input.event.type,
      deliveryId: input.deliveryId,
    });

    const { attempts, last } = await runWithRetry(input.endpointUrl, body, headers);

    if (last.ok) {
      await repos.deliveries.markDelivered(input.deliveryId, attempts, now());
      return {
        delivered: true,
        attempts,
        finalStatus: last.status,
        lastError: null,
      };
    }

    const errorMessage = last.errorMessage ?? "unknown delivery error";
    await repos.deliveries.markFailed(input.deliveryId, attempts, errorMessage);
    const failureId = newId();
    await repos.failures.insert({
      id: failureId,
      endpointId: input.endpointId,
      deliveryId: input.deliveryId,
      eventType: input.event.type,
      body,
      signatureHeader: headers["X-Livepeer-Signature"],
      attemptCount: attempts,
      lastError: errorMessage,
      statusCode: last.status,
      replayedAt: null,
    });
    return {
      delivered: false,
      attempts,
      finalStatus: last.status,
      lastError: errorMessage,
      failureId,
    };
  }

  async function replay(failureId: string): Promise<RetryDispatcherOutcome> {
    const failure = await repos.failures.byId(failureId);
    if (!failure) throw new Error(`webhook failure ${failureId} not found`);
    const endpoint = await repos.endpoints.byId(failure.endpointId);
    if (!endpoint) throw new Error(`endpoint ${failure.endpointId} not found`);

    const headers = signEvent({
      secret: endpoint.secret + deps.pepper,
      body: failure.body,
      eventType: failure.eventType,
      deliveryId: failure.deliveryId,
    });

    const { attempts, last } = await runWithRetry(endpoint.url, failure.body, headers);

    if (last.ok) {
      await repos.failures.markReplayed(failureId, now());
      return {
        delivered: true,
        attempts,
        finalStatus: last.status,
        lastError: null,
        failureId,
      };
    }
    return {
      delivered: false,
      attempts,
      finalStatus: last.status,
      lastError: last.errorMessage,
      failureId,
    };
  }

  return {
    dispatch: runDispatch,
    replayFailure: replay,
  };
}
