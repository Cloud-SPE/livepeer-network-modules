import {
  CopyObjectCommand,
  DeleteObjectCommand,
  GetObjectCommand,
  PutObjectCommand,
  S3Client,
} from "@aws-sdk/client-s3";
import { getSignedUrl } from "@aws-sdk/s3-request-presigner";

import type {
  PathForOpts,
  StorageProvider,
  StoragePathKind,
} from "../engine/index.js";

export interface S3StorageConfig {
  endpoint?: string;
  region: string;
  bucket: string;
  accessKeyId: string;
  secretAccessKey: string;
  forcePathStyle?: boolean;
}

const PATH_KIND_PREFIX: Record<StoragePathKind, string> = {
  source: "sources",
  rendition: "renditions",
  manifest: "manifests",
  thumbnail: "thumbnails",
  metadata: "metadata",
  liveSegment: "live/segments",
  liveManifest: "live/manifests",
};

export function loadS3ConfigFromEnv(env: NodeJS.ProcessEnv = process.env): S3StorageConfig {
  const region = env["S3_REGION"];
  const bucket = env["S3_BUCKET"];
  const accessKeyId = env["S3_ACCESS_KEY_ID"];
  const secretAccessKey = env["S3_SECRET_ACCESS_KEY"];
  if (!region) throw new Error("S3_REGION is required");
  if (!bucket) throw new Error("S3_BUCKET is required");
  if (!accessKeyId) throw new Error("S3_ACCESS_KEY_ID is required");
  if (!secretAccessKey) throw new Error("S3_SECRET_ACCESS_KEY is required");
  const cfg: S3StorageConfig = { region, bucket, accessKeyId, secretAccessKey };
  const endpoint = env["S3_ENDPOINT"];
  if (endpoint) cfg.endpoint = endpoint;
  cfg.forcePathStyle = env["S3_FORCE_PATH_STYLE"] !== "false";
  return cfg;
}

export function pathFor(opts: PathForOpts): string {
  const prefix = PATH_KIND_PREFIX[opts.kind];
  const segments: string[] = [prefix];
  if (opts.assetId) segments.push(opts.assetId);
  if (opts.streamId) segments.push(opts.streamId);
  if (opts.codec && opts.resolution) segments.push(`${opts.codec}-${opts.resolution}`);
  segments.push(opts.filename);
  return segments.join("/");
}

export function createS3StorageProvider(config: S3StorageConfig): StorageProvider {
  const clientCfg: ConstructorParameters<typeof S3Client>[0] = {
    region: config.region,
    credentials: {
      accessKeyId: config.accessKeyId,
      secretAccessKey: config.secretAccessKey,
    },
  };
  if (config.endpoint) clientCfg.endpoint = config.endpoint;
  if (config.forcePathStyle !== undefined) clientCfg.forcePathStyle = config.forcePathStyle;
  const client = new S3Client(clientCfg);
  const bucket = config.bucket;

  return {
    async putSignedUploadUrl(opts) {
      const storageKey = pathFor({
        assetId: opts.assetId,
        kind: opts.kind,
        filename: opts.filename,
      });
      const cmd = new PutObjectCommand({
        Bucket: bucket,
        Key: storageKey,
        ContentType: opts.contentType,
      });
      const url = await getSignedUrl(client, cmd, { expiresIn: opts.expiresInSec });
      return { url, storageKey };
    },

    async getSignedDownloadUrl(opts) {
      const cmd = new GetObjectCommand({ Bucket: bucket, Key: opts.storageKey });
      return getSignedUrl(client, cmd, { expiresIn: opts.expiresInSec });
    },

    async putObject(storageKey, body, opts) {
      const cmd = new PutObjectCommand({
        Bucket: bucket,
        Key: storageKey,
        Body: typeof body === "string" ? body : Buffer.from(body),
        ContentType: opts?.contentType,
      });
      await client.send(cmd);
    },

    async delete(storageKey) {
      const cmd = new DeleteObjectCommand({ Bucket: bucket, Key: storageKey });
      await client.send(cmd);
    },

    async copyObject(srcKey, dstKey) {
      const cmd = new CopyObjectCommand({
        Bucket: bucket,
        CopySource: `${bucket}/${srcKey}`,
        Key: dstKey,
      });
      await client.send(cmd);
    },

    pathFor(opts: PathForOpts) {
      return pathFor(opts);
    },
  };
}
