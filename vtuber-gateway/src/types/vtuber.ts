import { z } from "zod";

// Session-open request/response shapes. Ported from
// `livepeer-network-suite/livepeer-vtuber-gateway/src/types/vtuber.ts`
// per plan 0013-vtuber §5.1.

export const SessionOpenRequestSchema = z.object({
  persona: z.string().min(1),
  vrm_url: z.string().url(),
  llm_provider: z.string().min(1),
  tts_provider: z.string().min(1),
  target_youtube_broadcast: z.string().min(1).optional(),
  width: z.number().int().min(64).max(3840).default(1280),
  height: z.number().int().min(64).max(2160).default(720),
  target_fps: z.number().int().min(1).max(60).default(24),
  offering: z.string().min(1).optional(),
  ttl_seconds: z.number().int().positive().optional(),
});
export type SessionOpenRequest = z.infer<typeof SessionOpenRequestSchema>;

export const SessionOpenResponseSchema = z.object({
  session_id: z.string().uuid(),
  control_url: z.string().url(),
  expires_at: z.string(),
  session_child_bearer: z.string(),
});
export type SessionOpenResponse = z.infer<typeof SessionOpenResponseSchema>;

export const SessionStatusEnum = z.enum([
  "starting",
  "active",
  "ending",
  "ended",
  "errored",
]);
export type SessionStatus = z.infer<typeof SessionStatusEnum>;

export const SessionStatusResponseSchema = z.object({
  session_id: z.string().uuid(),
  status: SessionStatusEnum,
  error_code: z.string().nullable().optional(),
  expires_at: z.string(),
  ended_at: z.string().nullable().optional(),
});
export type SessionStatusResponse = z.infer<typeof SessionStatusResponseSchema>;

export const SessionTopupRequestSchema = z.object({
  cents: z.number().int().positive(),
});
export type SessionTopupRequest = z.infer<typeof SessionTopupRequestSchema>;

export const SessionEndRequestSchema = z.object({
  reason: z.string().optional(),
});
export type SessionEndRequest = z.infer<typeof SessionEndRequestSchema>;
