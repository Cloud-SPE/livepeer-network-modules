// Shared Fastify pre-handlers. Customer auth defers entirely to
// customer-portal's CustomerTokenService — it parses the Bearer token,
// hashes against the configured pepper, and throws typed errors on
// failure. Admin auth defers to whichever AdminAuthResolver was
// returned by createCustomerPortal.
export function customerAuthPreHandler(service) {
    return async (req, reply) => {
        try {
            req.customerSession = await service.authenticate(req.headers.authorization);
        }
        catch (err) {
            await reply.code(401).send({
                error: "authentication_failed",
                message: err instanceof Error ? err.message : String(err),
            });
        }
    };
}
export function adminAuthPreHandler(resolver) {
    return async (req, reply) => {
        const result = await resolver.resolve({
            headers: req.headers,
            ip: req.ip,
        });
        if (!result) {
            await reply.code(401).send({
                error: {
                    code: "authentication_failed",
                    message: "admin token + actor required",
                    type: "AdminAuthError",
                },
            });
            return;
        }
        req.adminActor = result.actor;
    };
}
//# sourceMappingURL=auth.js.map