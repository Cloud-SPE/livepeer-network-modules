// Per-session payment-daemon flow per Q7 lock — one ticket per
// session-open via `payerDaemon.createPayment(...)`. Source surface:
// `livepeer-network-suite/livepeer-vtuber-gateway/src/service/payments/
// {createPayment,vtuberSession}.ts`.

import type {
  CreatePaymentRequest,
  CreatePaymentResponse,
  PayerDaemonClient,
  ResolvedPaymentDescriptor,
} from "../../providers/payerDaemon.js";

export interface OpenSessionPaymentInput {
  faceValueWei: string;
  recipientEthAddress: string;
  capability: string;
  offering: string;
  nodeId: string;
}

export async function createSessionPayment(
  client: PayerDaemonClient,
  input: OpenSessionPaymentInput,
): Promise<ResolvedPaymentDescriptor> {
  const req: CreatePaymentRequest = {
    faceValueWei: input.faceValueWei,
    recipientEthAddress: input.recipientEthAddress,
    capability: input.capability,
    offering: input.offering,
    nodeId: input.nodeId,
  };
  const resp: CreatePaymentResponse = await client.createPayment(req);
  return {
    payerWorkId: resp.payerWorkId,
    paymentHeader: resp.paymentHeader,
    faceValueWei: input.faceValueWei,
    capability: input.capability,
    offering: input.offering,
    nodeId: input.nodeId,
    recipientEthAddress: input.recipientEthAddress,
  };
}
