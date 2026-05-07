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

export interface PayerDaemonClientDeps {
  socketPath: string;
  fetchImpl?: (url: string, init: RequestInit) => Promise<Response>;
}

export function createUnixSocketPayerDaemonClient(deps: PayerDaemonClientDeps): PayerDaemonClient {
  const baseUrl = `http://payer-daemon.invalid`;
  const fetchImpl = deps.fetchImpl;
  return {
    async createPayment(req) {
      if (!fetchImpl) {
        throw new Error(
          "payerDaemon over unix socket not yet implemented; supply a fetchImpl wired to the socket path " +
            deps.socketPath,
        );
      }
      const res = await fetchImpl(`${baseUrl}/v1/payments`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          face_value_wei: req.faceValueWei,
          recipient_eth_address: req.recipientEthAddress,
          capability: req.capability,
          offering: req.offering,
          node_id: req.nodeId,
        }),
      });
      if (!res.ok) {
        throw new Error(`payerDaemon createPayment returned ${res.status}`);
      }
      const body = (await res.json()) as {
        payer_work_id?: string;
        payment_header?: string;
      };
      if (!body.payer_work_id || !body.payment_header) {
        throw new Error("payerDaemon createPayment: malformed response");
      }
      return {
        payerWorkId: body.payer_work_id,
        paymentHeader: body.payment_header,
      };
    },
    async close() {},
  };
}
