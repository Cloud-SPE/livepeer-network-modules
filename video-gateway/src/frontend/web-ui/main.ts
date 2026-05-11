document.body.dataset.livepeerUiMode = "network-console";
document.body.dataset.livepeerSessionKey = "video-gateway:admin-session";

await import("@livepeer-rewrite/customer-portal-shared");
await import("./components/admin-app.js");
await import("./components/admin-customers.js");
await import("./components/admin-projects.js");
await import("./components/admin-customer-detail.js");
await import("./components/admin-customer-adjust.js");
await import("./components/admin-customer-refund.js");
await import("./components/admin-health.js");
await import("./components/admin-nodes.js");
await import("./components/admin-assets.js");
await import("./components/admin-topups.js");
await import("./components/admin-reservations.js");
await import("./components/admin-usage.js");
await import("./components/admin-audit.js");
await import("./components/admin-streams.js");
await import("./components/admin-webhooks.js");
await import("./components/admin-recordings.js");

export {};
