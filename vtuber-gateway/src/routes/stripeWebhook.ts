import type { FastifyInstance, FastifyPluginAsync } from "fastify";

import type { VtuberGatewayDeps } from "../runtime/deps.js";

export interface StripeWebhookRouteDeps {
  deps: VtuberGatewayDeps;
}

export const registerStripeWebhookRoutes: FastifyPluginAsync<
  StripeWebhookRouteDeps
> = async (app: FastifyInstance, { deps }: StripeWebhookRouteDeps) => {
  app.addContentTypeParser(
    "application/json",
    { parseAs: "buffer" },
    (req, body, done) => {
      if (req.url === "/v1/stripe/webhook") {
        done(null, body);
        return;
      }
      try {
        const text = (body as Buffer).toString("utf8");
        const json = text === "" ? null : JSON.parse(text);
        done(null, json);
      } catch (err) {
        done(err as Error, undefined);
      }
    },
  );

  app.post("/v1/stripe/webhook", async (req, reply) => {
    const sig = req.headers["stripe-signature"];
    if (typeof sig !== "string" || sig.length === 0) {
      await reply.code(400).send({ error: "missing_stripe_signature" });
      return;
    }
    if (deps.stripe === undefined || deps.webhookEventStore === undefined) {
      await reply.code(503).send({ error: "stripe_not_configured" });
      return;
    }
    const rawBody = (req.body ?? Buffer.alloc(0)) as Buffer;

    let event;
    try {
      event = deps.stripe.constructEvent(rawBody, sig);
    } catch {
      await reply.code(400).send({ error: "signature_invalid" });
      return;
    }

    const payloadJson = rawBody.toString("utf8");
    const isNew = await deps.webhookEventStore.insertIfNew(
      event.id,
      event.type,
      payloadJson,
    );
    if (!isNew) {
      await reply
        .code(200)
        .send({ outcome: "duplicate", event_type: event.type });
      return;
    }

    try {
      const handled = await dispatchVtuberEvent(deps.webhookEventStore, event);
      await reply.code(200).send({
        outcome: handled ? "processed" : "unsupported",
        event_type: event.type,
      });
    } catch (err) {
      req.log.error({ err, eventType: event.type }, "stripe webhook handler failed");
      await reply.code(500).send({ outcome: "handler_error" });
    }
  });
};

async function dispatchVtuberEvent(
  store: NonNullable<VtuberGatewayDeps["webhookEventStore"]>,
  event: { id: string; type: string; data: { object: Record<string, unknown> } },
): Promise<boolean> {
  if (event.type === "checkout.session.completed") {
    const obj = event.data.object as {
      client_reference_id?: string;
      metadata?: { customer_id?: string };
      amount_total?: number;
      id?: string;
    };
    const customerId = obj.client_reference_id ?? obj.metadata?.customer_id;
    const sessionId = obj.id;
    const amount = obj.amount_total;
    if (
      typeof customerId !== "string" ||
      typeof sessionId !== "string" ||
      typeof amount !== "number"
    ) {
      throw new Error(
        "checkout.session.completed missing customer_id / session_id / amount",
      );
    }
    await store.creditTopup({
      customerId,
      stripeSessionId: sessionId,
      amountUsdCents: BigInt(amount),
    });
    return true;
  }
  if (event.type === "charge.dispute.created") {
    const obj = event.data.object as {
      metadata?: { stripe_session_id?: string };
    };
    const stripeSessionId = obj.metadata?.stripe_session_id;
    if (typeof stripeSessionId === "string") {
      await store.markTopupDisputed(stripeSessionId);
      return true;
    }
    return false;
  }
  return false;
}
