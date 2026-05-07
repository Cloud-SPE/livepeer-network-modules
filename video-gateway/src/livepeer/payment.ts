export interface PaymentBuildInput {
  callerId: string;
  capability: string;
  offering: string;
  workUnits: bigint;
}

export interface PaymentTicket {
  header: string;
}

export async function buildPayment(_input: PaymentBuildInput): Promise<PaymentTicket> {
  return { header: "" };
}
