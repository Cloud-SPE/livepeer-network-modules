import type {
  AuthResolver,
  AdminAuthResolver,
  EventBus,
  Logger,
  RateLimiter,
  StorageProvider,
  StreamKeyHasher,
  Wallet,
  WebhookSink,
  WorkerClient,
  WorkerResolver,
} from "../interfaces/index.js";
import type {
  AssetRepo,
  EncodingJobRepo,
  LiveStreamRepo,
  PlaybackIdRepo,
  RenditionRepo,
  UploadRepo,
} from "../repo/index.js";
import type { EncodingLadder } from "../config/encodingLadder.js";
import type { PricingConfig } from "../config/pricing.js";
import type { Caller } from "../types/index.js";

export interface DispatchCommon {
  caller: Caller;
  wallet: Wallet;
  storage: StorageProvider;
  workerResolver: WorkerResolver;
  workerClient: WorkerClient;
  webhookSink: WebhookSink;
  eventBus: EventBus;
  assetRepo: AssetRepo;
  uploadRepo: UploadRepo;
  liveStreamRepo: LiveStreamRepo;
  playbackIdRepo: PlaybackIdRepo;
  jobRepo: EncodingJobRepo;
  renditionRepo: RenditionRepo;
  pricing: PricingConfig;
  ladder: EncodingLadder;
  logger?: Logger;
  rateLimiter?: RateLimiter;
  playbackBaseUrl: string;
  ingestBaseUrl: string;
  workerSelectionMinWeight?: number;
  streamKeyHasher: StreamKeyHasher;
}

export type { AuthResolver, AdminAuthResolver };
