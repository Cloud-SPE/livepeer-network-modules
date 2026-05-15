import { recentForCustomer, summaryForCustomer, } from "../repo/usage.js";
import { customerAuthPreHandler } from "../middleware/auth.js";
const THIRTY_DAYS_MS = 30 * 24 * 60 * 60 * 1_000;
export function registerUsageRoutes(app, deps) {
    const requireCustomer = customerAuthPreHandler(deps.portal.customerTokenService);
    app.get("/portal/usage/summary", { preHandler: requireCustomer }, async (req, reply) => {
        const customerId = req.customerSession.customer.id;
        const windowStart = new Date(Date.now() - THIRTY_DAYS_MS);
        const summary = await summaryForCustomer(deps.db, customerId, windowStart);
        await reply.send({
            window_days: 30,
            total_sessions: summary.totalSessions,
            total_seconds: summary.totalSeconds,
        });
    });
    app.get("/portal/usage/recent", { preHandler: requireCustomer }, async (req, reply) => {
        const customerId = req.customerSession.customer.id;
        const rows = await recentForCustomer(deps.db, customerId, 50);
        await reply.send({
            events: rows.map((r) => ({
                id: r.id,
                session_id: r.sessionId,
                orchestrator: r.orchestrator,
                started_at: r.startedAt.toISOString(),
                ended_at: r.endedAt?.toISOString() ?? null,
                duration_seconds: r.durationSeconds,
            })),
        });
    });
}
//# sourceMappingURL=usage.js.map