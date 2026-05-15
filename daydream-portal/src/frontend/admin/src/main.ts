// Daydream admin SPA bootstrap. Pulls in the shared widget catalog
// (auto-installs global styles) and registers the daydream-specific
// admin components.

import "@livepeer-network-modules/customer-portal-shared";

document.body.dataset.livepeerUiMode = "network-console";

import "./components/admin-daydream-app.js";
import "./components/admin-daydream-login.js";
import "./components/admin-daydream-signups.js";
import "./components/admin-daydream-usage.js";
