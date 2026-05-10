import "@livepeer-rewrite/customer-portal-shared";
import "./components/admin-app.js";
import "./components/admin-customers.js";
import "./components/admin-customer-detail.js";
import "./components/admin-customer-adjust.js";
import "./components/admin-customer-refund.js";
import "./components/admin-health.js";
import "./components/admin-assets.js";
import "./components/admin-topups.js";
import "./components/admin-reservations.js";
import "./components/admin-audit.js";
import "./components/admin-streams.js";
import "./components/admin-webhooks.js";
import "./components/admin-recordings.js";

document.body.dataset.livepeerUiMode = "network-console";
document.body.dataset.livepeerSessionKey = "video-gateway:admin-session";
