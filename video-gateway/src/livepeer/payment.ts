import type { PayerDaemonClient } from "./payerDaemonClient.js";

export interface PaymentBuildInput {
  callerId: string;
  capability: string;
  offering: string;
  workUnits: bigint;
  faceValueWei: string;
  recipientEthAddress: string;
  nodeId: string;
}

export interface PaymentTicket {
  header: string;
  workId: string;
}

export interface PaymentBuilderDeps {
  payerDaemon: PayerDaemonClient;
}

export function createPaymentBuilder(deps: PaymentBuilderDeps) {
  return async function buildPayment(input: PaymentBuildInput): Promise<PaymentTicket> {
    const resp = await deps.payerDaemon.createPayment({
      faceValueWei: input.faceValueWei,
      recipientEthAddress: input.recipientEthAddress,
      capability: input.capability,
      offering: input.offering,
      nodeId: input.nodeId,
    });
    return { header: resp.paymentHeader, workId: resp.payerWorkId };
  };
}

export async function buildPayment(_input: PaymentBuildInput): Promise<PaymentTicket> {
  throw new Error("buildPayment requires a payerDaemon-bound builder; use createPaymentBuilder");
}
