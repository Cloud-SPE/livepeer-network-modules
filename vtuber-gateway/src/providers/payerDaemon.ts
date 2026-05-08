import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";

const PROTO_FILES = [
  "livepeer/payments/v1/types.proto",
  "livepeer/payments/v1/payer_daemon.proto",
];

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

interface GrpcPayerDaemonClient extends grpc.Client {
  createPayment(
    req: {
      faceValue: Buffer;
      recipient: Buffer;
      capability: string;
      offering: string;
    },
    cb: (
      err: grpc.ServiceError | null,
      resp: { paymentBytes: Buffer; expectedValue: Buffer },
    ) => void,
  ): void;
  health(
    req: Record<string, never>,
    cb: (err: grpc.ServiceError | null, resp: { status: string }) => void,
  ): void;
}

interface PayerDaemonProto {
  livepeer: { payments: { v1: { PayerDaemon: grpc.ServiceClientConstructor } } };
}

export async function createGrpcPayerDaemonClient(input: {
  socketPath: string;
  protoRoot: string;
}): Promise<PayerDaemonClient> {
  const def = await protoLoader.load(PROTO_FILES, {
    keepCase: false,
    longs: String,
    enums: String,
    defaults: true,
    oneofs: true,
    includeDirs: [input.protoRoot],
  });
  const proto = grpc.loadPackageDefinition(def) as unknown as PayerDaemonProto;
  const ClientCtor = proto.livepeer.payments.v1.PayerDaemon;
  const client = new ClientCtor(
    `unix:${input.socketPath}`,
    grpc.credentials.createInsecure(),
  ) as unknown as GrpcPayerDaemonClient;

  await new Promise<void>((resolve, reject) => {
    client.health({}, (err) => (err ? reject(err) : resolve()));
  });

  return {
    async createPayment(req: CreatePaymentRequest): Promise<CreatePaymentResponse> {
      const resp = await new Promise<{ paymentBytes: Buffer }>((resolve, reject) => {
        client.createPayment(
          {
            faceValue: bigintToBigEndian(BigInt(req.faceValueWei)),
            recipient: hexToBuffer(req.recipientEthAddress),
            capability: req.capability,
            offering: req.offering,
          },
          (err, result) => (err ? reject(err) : resolve(result)),
        );
      });
      return {
        payerWorkId: "",
        paymentHeader: Buffer.from(resp.paymentBytes).toString("base64"),
      };
    },
    async close(): Promise<void> {
      client.close();
    },
  };
}

function bigintToBigEndian(n: bigint): Buffer {
  if (n === 0n) return Buffer.alloc(0);
  const bytes: number[] = [];
  let v = n;
  while (v > 0n) {
    bytes.unshift(Number(v & 0xffn));
    v >>= 8n;
  }
  return Buffer.from(bytes);
}

function hexToBuffer(hex: string): Buffer {
  return Buffer.from(hex.replace(/^0x/, ""), "hex");
}
