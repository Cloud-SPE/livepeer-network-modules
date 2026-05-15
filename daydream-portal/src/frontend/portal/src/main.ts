// Daydream portal SPA bootstrap. Imports custom-element registrations
// for the daydream-specific pages plus the shared widget catalog from
// @livepeer-network-modules/customer-portal-shared (which auto-installs
// its global styles on import).

import "@livepeer-network-modules/customer-portal-shared";

document.body.dataset.livepeerUiMode = "product-app";

import "./components/portal-daydream-app.js";
import "./components/portal-daydream-waitlist.js";
import "./components/portal-daydream-login.js";
import "./components/portal-daydream-playground.js";
import "./components/portal-daydream-prompts.js";
import "./components/portal-daydream-usage.js";
