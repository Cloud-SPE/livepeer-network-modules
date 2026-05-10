import type { auth, billing } from "@livepeer-rewrite/customer-portal";

import { loadRateCardSnapshot, type Queryable } from "../repo/rateCard.js";
import {
  computeChatActualCost,
  estimateChatReservation,
  type ChatReservationEstimate,
} from "./pricing/cost.js";
import type { RateCardSnapshot } from "./pricing/types.js";

type Caller = auth.Caller;
type ReservationHandle = billing.ReservationHandle;
type Wallet = billing.Wallet;

export interface ChatBillingBody {
  model?: string;
  messages?: unknown;
  max_tokens?: unknown;
  max_completion_tokens?: unknown;
  stream_options?: Record<string, unknown>;
}

interface ChatUsage {
  promptTokens: number;
  completionTokens: number;
  totalTokens: number;
}

export interface ChatStreamSettlement {
  mode: "committed" | "refunded" | "partial";
  usage?: ChatUsage;
}

export interface CreateChatBillingServiceInput {
  wallet: Wallet;
  rateCardStore: Queryable;
  cacheTtlMs?: number;
  now?: () => number;
}

export function createChatBillingService(input: CreateChatBillingServiceInput) {
  const ttl = input.cacheTtlMs ?? 30_000;
  const now = input.now ?? Date.now;
  let cached: { snapshot: RateCardSnapshot; loadedAt: number } | null = null;

  async function getSnapshot(): Promise<RateCardSnapshot> {
    if (cached && now() - cached.loadedAt < ttl) return cached.snapshot;
    const snapshot = await loadRateCardSnapshot(input.rateCardStore);
    cached = { snapshot, loadedAt: now() };
    return snapshot;
  }

  return {
    async reserve(
      caller: Caller,
      workId: string,
      model: string,
      body: ChatBillingBody,
    ): Promise<ReservationHandle | null> {
      const callerTier = asCallerTier(caller.tier);
      const snapshot = await getSnapshot();
      const maxCompletionTokens = resolveMaxCompletionTokens(body, callerTier);
      const estimate = estimateChatReservation(
        serializePrompt(body.messages),
        maxCompletionTokens,
        model,
        snapshot,
      );
      return input.wallet.reserve(caller.id, {
        workId,
        cents: estimate.estCents,
        estimatedTokens: estimate.promptEstimateTokens + estimate.maxCompletionTokens,
        model,
        capability: "openai:chat-completions",
        callerTier,
      });
    },

    async commitFromResponseBody(
      caller: Caller,
      handle: ReservationHandle | null,
      model: string,
      rawBody: ArrayBuffer,
    ): Promise<ChatUsage> {
      if (!handle) {
        return parseResponseUsage(Buffer.from(rawBody).toString("utf8"));
      }
      const usage = parseResponseUsage(Buffer.from(rawBody).toString("utf8"));
      await commitUsage(input.wallet, await getSnapshot(), caller, handle, model, usage);
      return usage;
    },

    async settleStream(
      caller: Caller,
      handle: ReservationHandle | null,
      model: string,
      body: ChatBillingBody,
      transcript: string,
    ): Promise<ChatStreamSettlement> {
      if (!handle) {
        const analysis = analyzeStreamTranscript(transcript);
        return analysis.usage
          ? { mode: "committed", usage: analysis.usage }
          : analysis.firstTokenDelivered
            ? { mode: "partial" }
            : { mode: "refunded" };
      }

      const analysis = analyzeStreamTranscript(transcript);
      if (analysis.usage) {
        await commitUsage(input.wallet, await getSnapshot(), caller, handle, model, analysis.usage);
        return { mode: "committed", usage: analysis.usage };
      }

      if (!analysis.firstTokenDelivered) {
        await input.wallet.refund(handle);
        return { mode: "refunded" };
      }

      const snapshot = await getSnapshot();
      const callerTier = asCallerTier(caller.tier);
      const maxCompletionTokens = resolveMaxCompletionTokens(body, callerTier);
      const estimate = estimateChatReservation(
        serializePrompt(body.messages),
        maxCompletionTokens,
        model,
        snapshot,
      );
      const completionTokens = estimateCompletionTokens(analysis.deliveredText, estimate);
      const usage: ChatUsage = {
        promptTokens: estimate.promptEstimateTokens,
        completionTokens,
        totalTokens: estimate.promptEstimateTokens + completionTokens,
      };
      await commitUsage(input.wallet, snapshot, caller, handle, model, usage);
      return { mode: "partial", usage };
    },

    async refund(handle: ReservationHandle | null): Promise<void> {
      if (!handle) return;
      await input.wallet.refund(handle);
    },
  };
}

async function commitUsage(
  wallet: Wallet,
  snapshot: RateCardSnapshot,
  caller: Caller,
  handle: ReservationHandle,
  model: string,
  usage: ChatUsage,
): Promise<void> {
  const cost = computeChatActualCost(
    usage.promptTokens,
    usage.completionTokens,
    model,
    snapshot,
  );
  await wallet.commit(handle, {
    cents: cost.actualCents,
    actualTokens: usage.totalTokens,
    model,
    capability: "openai:chat-completions",
  });
  void caller;
}

function parseResponseUsage(body: string): ChatUsage {
  let parsed: unknown;
  try {
    parsed = JSON.parse(body);
  } catch (err) {
    throw new Error(`chat billing: invalid JSON response body (${String(err)})`);
  }
  const usage = usageFromObject(parsed);
  if (!usage) throw new Error("chat billing: response missing usage");
  return usage;
}

function analyzeStreamTranscript(transcript: string): {
  usage: ChatUsage | null;
  firstTokenDelivered: boolean;
  deliveredText: string;
} {
  const frames = transcript.split(/\r?\n\r?\n/);
  let usage: ChatUsage | null = null;
  let firstTokenDelivered = false;
  let deliveredText = "";

  for (const frame of frames) {
    if (!frame.includes("data:")) continue;
    const payload = frame
      .split(/\r?\n/)
      .filter((line) => line.startsWith("data:"))
      .map((line) => line.slice(5).trimStart())
      .join("\n");
    if (!payload || payload === "[DONE]") continue;
    let parsed: unknown;
    try {
      parsed = JSON.parse(payload);
    } catch {
      continue;
    }
    const parsedUsage = usageFromObject(parsed);
    if (parsedUsage) {
      usage = parsedUsage;
      continue;
    }
    const delta = extractDeltaContent(parsed);
    if (delta.length > 0) {
      firstTokenDelivered = true;
      deliveredText += delta;
    }
  }

  return { usage, firstTokenDelivered, deliveredText };
}

function usageFromObject(value: unknown): ChatUsage | null {
  if (!value || typeof value !== "object") return null;
  const usage = (value as { usage?: unknown }).usage;
  if (!usage || typeof usage !== "object") return null;
  const promptTokens = asNumber((usage as { prompt_tokens?: unknown }).prompt_tokens);
  const completionTokens = asNumber((usage as { completion_tokens?: unknown }).completion_tokens);
  const totalTokens = asNumber((usage as { total_tokens?: unknown }).total_tokens);
  if (promptTokens === null || completionTokens === null || totalTokens === null) return null;
  return { promptTokens, completionTokens, totalTokens };
}

function extractDeltaContent(value: unknown): string {
  if (!value || typeof value !== "object") return "";
  const choices = (value as { choices?: unknown }).choices;
  if (!Array.isArray(choices) || choices.length === 0) return "";
  const first = choices[0];
  if (!first || typeof first !== "object") return "";
  const delta = (first as { delta?: unknown }).delta;
  if (!delta || typeof delta !== "object") return "";
  const content = (delta as { content?: unknown }).content;
  return typeof content === "string" ? content : "";
}

function asNumber(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function asCallerTier(value: string): "free" | "prepaid" {
  if (value === "free" || value === "prepaid") return value;
  throw new Error(`chat billing: unsupported caller tier ${value}`);
}

function resolveMaxCompletionTokens(
  body: ChatBillingBody,
  callerTier: "free" | "prepaid",
): number {
  const candidate = firstPositiveInteger(body.max_completion_tokens, body.max_tokens);
  if (candidate !== null) return candidate;
  return callerTier === "free" ? 1024 : 32_768;
}

function firstPositiveInteger(...values: unknown[]): number | null {
  for (const value of values) {
    if (typeof value === "number" && Number.isInteger(value) && value > 0) return value;
  }
  return null;
}

function serializePrompt(messages: unknown): string {
  if (!Array.isArray(messages)) return "";
  return messages
    .map((message) => {
      if (!message || typeof message !== "object") return "";
      const role = typeof (message as { role?: unknown }).role === "string"
        ? (message as { role: string }).role
        : "unknown";
      const content = flattenContent((message as { content?: unknown }).content);
      return `${role}:${content}`;
    })
    .join("\n");
}

function flattenContent(value: unknown): string {
  if (typeof value === "string") return value;
  if (!Array.isArray(value)) return "";
  return value
    .map((part) => {
      if (typeof part === "string") return part;
      if (!part || typeof part !== "object") return "";
      if (typeof (part as { text?: unknown }).text === "string") {
        return (part as { text: string }).text;
      }
      return "";
    })
    .filter((part) => part.length > 0)
    .join(" ");
}

function estimateCompletionTokens(
  deliveredText: string,
  estimate: ChatReservationEstimate,
): number {
  const fromText = deliveredText.trim().length > 0 ? Math.ceil(deliveredText.length / 4) : 0;
  if (fromText > 0) return fromText;
  return Math.max(1, Math.floor(estimate.maxCompletionTokens / 2));
}
