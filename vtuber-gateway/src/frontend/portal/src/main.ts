// Vtuber portal SPA bootstrap. Imports custom-element registrations
// for the vtuber-specific pages; Vite build + router lands in a
// follow-up commit.

document.body.dataset.livepeerUiMode = "product-app";

import "./components/portal-vtuber-app.js";
import "./components/portal-vtuber-sessions.js";
import "./components/portal-vtuber-persona.js";
import "./components/portal-vtuber-history.js";
