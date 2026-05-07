export interface StorageProvider {
  putSignedUploadUrl(opts: {
    assetId: string;
    kind: StoragePathKind;
    filename: string;
    contentType: string;
    expiresInSec: number;
  }): Promise<{ url: string; storageKey: string }>;

  getSignedDownloadUrl(opts: {
    storageKey: string;
    expiresInSec: number;
  }): Promise<string>;

  putObject(
    storageKey: string,
    body: Uint8Array | string,
    opts?: { contentType?: string },
  ): Promise<void>;

  delete(storageKey: string): Promise<void>;

  copyObject(srcKey: string, dstKey: string): Promise<void>;

  pathFor(opts: PathForOpts): string;
}

export type StoragePathKind =
  | "source"
  | "rendition"
  | "manifest"
  | "thumbnail"
  | "metadata"
  | "liveSegment"
  | "liveManifest";

export interface PathForOpts {
  assetId?: string;
  streamId?: string;
  kind: StoragePathKind;
  codec?: string;
  resolution?: string;
  filename: string;
}
