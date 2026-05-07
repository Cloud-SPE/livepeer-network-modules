export class VideoCoreError extends Error {
  readonly code: VideoCoreErrorCode;
  readonly details?: unknown;

  constructor(code: VideoCoreErrorCode, message: string, details?: unknown) {
    super(message);
    this.name = "VideoCoreError";
    this.code = code;
    this.details = details;
  }
}

export type VideoCoreErrorCode =
  | "NotFound"
  | "NotImplemented"
  | "Unauthorized"
  | "Forbidden"
  | "BadRequest"
  | "Conflict"
  | "PaymentRequired"
  | "TooManyRequests"
  | "NoWorkersAvailable"
  | "WorkerError"
  | "StorageError"
  | "WalletReserveFailed"
  | "InternalError";
