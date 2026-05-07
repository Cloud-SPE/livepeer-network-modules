/** Endpoint-level config shared across all mode middleware functions. */
export interface BrokerEndpoint {
  /**
   * Base URL of the broker (e.g. `http://broker:8080`). The middleware
   * appends mode-specific paths (e.g. `/v1/cap`).
   */
  url: string;

  /**
   * AbortSignal for cancellation; passed through to fetch / undici.
   */
  signal?: AbortSignal;
}

/** Common per-call inputs every mode middleware needs. */
export interface BrokerCall {
  /** Capability ID — set as `Livepeer-Capability`. */
  capability: string;
  /** Offering ID — set as `Livepeer-Offering`. */
  offering: string;
  /** Base64-encoded `Livepeer-Payment` envelope (gateway-side responsibility). */
  paymentBlob: string;
  /** Optional request-id for correlation; set as `Livepeer-Request-Id`. */
  requestId?: string;
}

/** Common pieces every mode response surfaces. */
export interface BrokerResponseEnvelope {
  /** HTTP status returned by the broker. */
  status: number;
  /** Work units the broker reported (0 if not present). */
  workUnits: number;
  /** Echoed `Livepeer-Request-Id`. */
  requestId: string | undefined;
}
