document.body.dataset.livepeerUiMode = "product-app";
document.body.dataset.livepeerSessionKey = "video-gateway:portal-session";

await import("@livepeer-rewrite/customer-portal-shared");
await import("./components/portal-app.js");
await import("./components/portal-assets.js");
await import("./components/portal-streams.js");
await import("./components/portal-webhooks.js");
await import("./components/portal-recordings.js");
