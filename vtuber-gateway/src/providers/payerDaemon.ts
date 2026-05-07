// payment-daemon (sender) gRPC client. The gateway dials a local
// payer-daemon over the unix-socket given by
// LIVEPEER_PAYER_DAEMON_SOCKET. Per plan 0013-vtuber Q7 lock the
// gateway emits ONE ticket per session-open via
// payerDaemon.createPayment(faceValueWei, recipient, capability,
// offering, nodeId) and reconciles per-second debits against it.
//
// Source: `livepeer-network-suite/livepeer-vtuber-gateway/src/providers/payerDaemon.ts`.

export interface CreatePaymentRequest {
  faceValueWei: string;
  recipientEthAddress: string;
  capability: string;
  offering: string;
  nodeId: string;
}

export interface CreatePaymentResponse {
  payerWorkId: string;
  paymentHeader: string;
}

export interface PayerDaemonClient {
  createPayment(req: CreatePaymentRequest): Promise<CreatePaymentResponse>;
  close(): Promise<void>;
}

export interface ResolvedPaymentDescriptor {
  payerWorkId: string;
  paymentHeader: string;
  faceValueWei: string;
  capability: string;
  offering: string;
  nodeId: string;
  recipientEthAddress: string;
}
