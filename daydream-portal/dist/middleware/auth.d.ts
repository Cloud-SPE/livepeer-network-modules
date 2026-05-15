import type { preHandlerAsyncHookHandler } from "fastify";
import type { CustomerPortal, auth } from "@livepeer-network-modules/customer-portal";
export declare function customerAuthPreHandler(service: CustomerPortal["customerTokenService"]): preHandlerAsyncHookHandler;
export declare function adminAuthPreHandler(resolver: auth.AdminAuthResolver): preHandlerAsyncHookHandler;
