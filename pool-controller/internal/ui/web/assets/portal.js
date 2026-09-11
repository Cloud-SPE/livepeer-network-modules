/* portal.js — the member portal's client.

   Same idiom as the operator console's console.js: no framework, no build
   step, one page-aware script, the same $ / api / card / setStatus helpers.

   Two things differ, and both come from the API rather than from taste.

   First, authorisation. Every enrollment-scoped member route authenticates
   with the host's enrollment bearer token, not the browser session, so this
   script has to hold those tokens to read a host's own status. They live in
   localStorage under portalStoreKey, the member is told so on Settings, and
   Settings can clear them. The controller stores only a hash and cannot hand
   one back, which is exactly why losing the browser copy means rotating.

   Second, privacy. Nothing here reads a pool-wide figure, because a member's
   own share next to a pool total is another member's income by subtraction.
   Every number on these pages is the member's own. */

(() => {
  const $ = (id) => document.getElementById(id);
  const on = (id, ev, fn) => { const el = $(id); if (el) el.addEventListener(ev, fn); };
  const val = (id) => { const el = $(id); return el ? el.value.trim() : ""; };
  const statusEl = $("status") || $("signinStatus");

  const portalStoreKey = "pool-member-hosts";

  let hosts = [];
  let hostStatuses = [];

  function setStatus(msg, cls = "") {
    if (!statusEl) return;
    statusEl.hidden = false;
    statusEl.className = "message" + (cls === "bad" || cls === "error" ? " message-error" : "");
    statusEl.textContent = msg;
  }

  // Card bodies are assembled as HTML strings and every value in them comes
  // from a response or from something the member typed, so it goes through
  // here first.
  function esc(value) {
    return String(value === null || value === undefined ? "" : value)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#39;");
  }

  async function api(path, opts = {}, token = "") {
    const headers = {};
    if (opts.body !== undefined) headers["Content-Type"] = "application/json";
    if (token) headers["Authorization"] = "Bearer " + token;
    const response = await fetch(path, {
      ...opts,
      headers: { ...headers, ...(opts.headers || {}) }
    });
    if (!response.ok) {
      const text = await response.text();
      const err = new Error((text || "").trim() || ("HTTP " + response.status));
      err.status = response.status;
      throw err;
    }
    const contentType = response.headers.get("content-type") || "";
    if (contentType.includes("application/json")) return response.json();
    return response.text();
  }

  function card(html) {
    const div = document.createElement("div");
    div.className = "card";
    div.innerHTML = html;
    return div;
  }

  function renderCards(hostID, items, render, emptyHTML) {
    const host = $(hostID);
    if (!host) return;
    host.innerHTML = "";
    if (!items || !items.length) {
      const empty = document.createElement("div");
      empty.className = "empty-state";
      empty.innerHTML = emptyHTML || '<span class="muted">Nothing to show.</span>';
      host.appendChild(empty);
      return;
    }
    items.forEach((item) => host.appendChild(card(render(item))));
  }

  // ---------- formatting ----------

  // A Go zero time marshals as 0001-01-01T00:00:00Z rather than being
  // omitted, so "never" has to be recognised rather than assumed absent.
  function hasTime(iso) {
    if (!iso) return false;
    const ms = Date.parse(iso);
    return !Number.isNaN(ms) && ms > Date.parse("2000-01-01T00:00:00Z");
  }

  function timeLabel(iso) {
    return hasTime(iso) ? new Date(iso).toLocaleString() : "never";
  }

  function agoLabel(iso) {
    if (!hasTime(iso)) return "never";
    const seconds = Math.max(0, Math.round((Date.now() - Date.parse(iso)) / 1000));
    if (seconds < 90) return seconds + "s ago";
    const minutes = Math.round(seconds / 60);
    if (minutes < 90) return minutes + "m ago";
    const hours = Math.round(minutes / 60);
    if (hours < 48) return hours + "h ago";
    return Math.round(hours / 24) + "d ago";
  }

  // Wei are exact and can exceed Number's safe range, so the ETH rendering
  // is done on BigInt and the exact wei string is always shown beside it.
  function ethLabel(wei) {
    let value;
    try {
      value = BigInt(String(wei === null || wei === undefined ? "0" : wei).trim() || "0");
    } catch (_) {
      return String(wei || "0");
    }
    const negative = value < 0n;
    if (negative) value = -value;
    const unit = 1000000000000000000n;
    const whole = (value / unit).toString();
    const fraction = (value % unit).toString().padStart(18, "0").replace(/0+$/, "");
    return (negative ? "-" : "") + whole + (fraction ? "." + fraction : "") + " ETH";
  }

  function weiLabel(wei) {
    return String(wei === null || wei === undefined ? "0" : wei) + " wei";
  }

  function sharePPMLabel(ppm) {
    const value = Number(ppm || 0);
    if (!value) return "";
    return "share cap " + (value / 10000).toFixed(2) + "% (" + value + " ppm)";
  }

  const okStates = ["active", "certified", "paid", "running"];
  const warnStates = ["pending", "testing", "probationary", "draining", "certifying", "pending_approval", "approved", "drafted", "registered"];
  const badStates = ["throttled", "suspended", "retired", "revoked", "failed", "held", "recertify"];

  function stateTone(state) {
    const key = String(state || "").toLowerCase();
    if (okStates.includes(key)) return "ok";
    if (warnStates.includes(key)) return "warn";
    if (badStates.includes(key)) return "bad";
    return "";
  }

  function statePill(state) {
    const tone = stateTone(state);
    return '<span class="pill' + (tone ? " pill-" + tone : "") + '">' + esc(state || "unknown") + "</span>";
  }

  // ---------- host credential store ----------

  function loadHosts() {
    try {
      const raw = window.localStorage.getItem(portalStoreKey);
      const parsed = raw ? JSON.parse(raw) : [];
      return Array.isArray(parsed) ? parsed.filter((item) => item && item.id && item.token) : [];
    } catch (_) {
      return [];
    }
  }

  function saveHosts(items) {
    hosts = items;
    try {
      window.localStorage.setItem(portalStoreKey, JSON.stringify(items));
    } catch (_) {
      setStatus("This browser refused to store the host credential, so the portal will forget it when the page closes.", "bad");
    }
  }

  function rememberHost(id, token, label) {
    const next = loadHosts().filter((item) => item.id !== id);
    next.push({ id: id, token: token, label: label || "", added_at: new Date().toISOString() });
    saveHosts(next);
  }

  function forgetHost(id) {
    saveHosts(loadHosts().filter((item) => item.id !== id));
  }

  function hostByID(id) {
    return hosts.find((item) => item.id === id) || null;
  }

  function hostName(host) {
    return (host && (host.label || host.id)) || "this host";
  }

  // ---------- inline confirmation ----------

  // Rotate and retire open the consequence in place rather than in a native
  // dialog, so what the member is agreeing to is on screen beside the button
  // that does it. A phrase to type is required where the action cannot be
  // undone by clicking again.
  function confirmInline(anchor, options) {
    const scope = anchor.closest(".card") || anchor.closest("section") || anchor.parentElement;
    if (!scope) return;
    const previous = scope.querySelector(".confirm");
    if (previous) previous.remove();
    const box = document.createElement("div");
    box.className = "confirm";
    box.innerHTML =
      "<strong>" + esc(options.title) + "</strong>" +
      "<p>" + options.bodyHTML + "</p>" +
      (options.phrase
        ? '<label class="small">Type <code>' + esc(options.phrase) + "</code> to continue<input data-confirm-phrase autocomplete=\"off\" spellcheck=\"false\"></label>"
        : "") +
      '<div class="actions">' +
        '<button data-confirm-go class="primary-button">' + esc(options.confirmLabel) + "</button>" +
        '<button data-confirm-cancel class="secondary">Cancel</button>' +
      "</div>";
    scope.appendChild(box);
    const phraseInput = box.querySelector("[data-confirm-phrase]");
    if (phraseInput) phraseInput.focus();
    box.querySelector("[data-confirm-cancel]").onclick = () => box.remove();
    box.querySelector("[data-confirm-go]").onclick = async () => {
      if (options.phrase) {
        const typed = phraseInput ? phraseInput.value.trim() : "";
        if (typed !== options.phrase) {
          setStatus("Type " + options.phrase + " exactly to confirm.", "bad");
          return;
        }
      }
      box.remove();
      await options.run();
    };
  }

  // ---------- copy ----------

  // app.js binds [data-copy-value] once at load; portal cards are built after
  // it, so they use their own attribute and a delegated handler rather than
  // being bound twice.
  document.addEventListener("click", async (event) => {
    const button = event.target.closest ? event.target.closest("[data-portal-copy]") : null;
    if (!button) return;
    const original = button.textContent;
    try {
      await navigator.clipboard.writeText(button.getAttribute("data-portal-copy") || "");
      button.textContent = "Copied";
      button.classList.add("copied");
      window.setTimeout(() => {
        button.textContent = original;
        button.classList.remove("copied");
      }, 1200);
    } catch (_) {
      setStatus("This browser would not let the page copy to the clipboard; select the text instead.", "bad");
    }
  });

  // ---------- credential panel ----------

  // The token is returned once. The controller keeps a hash of it and has no
  // way to show it again, so the panel says that plainly and stays open until
  // the member dismisses it.
  function credentialPanelHTML(enrollmentID, token, heading) {
    return '<div class="secret-panel">' +
      "<h3>" + esc(heading) + "</h3>" +
      '<p class="small">This is the only time this token is shown. The controller stores a hash of it and cannot show it again — if it is lost, the only way back is to rotate it from a browser that still has it, or to have the operator revoke the host.</p>' +
      '<div class="mono">enrollment ' + esc(enrollmentID) + "</div>" +
      "<pre>" + esc(token) + "</pre>" +
      '<div class="actions">' +
        '<button class="copy-button" data-portal-copy="' + esc(token) + '">Copy token</button>' +
        '<button class="secondary" data-secret-dismiss>I have saved it</button>' +
      "</div>" +
      '<p class="small">The bundle you download below already contains this token, and this browser keeps a copy so the portal can read the host\'s status. Settings can clear the browser copy at any time.</p>' +
      "</div>";
  }

  function bindSecretDismiss(root) {
    if (!root) return;
    root.querySelectorAll("[data-secret-dismiss]").forEach((button) => {
      button.onclick = () => {
        const panel = button.closest(".secret-panel");
        if (panel) panel.remove();
      };
    });
  }

  // ---------- sign-in page ----------

  function initSignIn() {
    const form = $("signinForm");
    if (!form) return;
    let pendingNonce = null;

    async function requestNonce(address) {
      const result = await api("/member/v1/auth/nonce", {
        method: "POST",
        body: JSON.stringify({ member_eth_address: address })
      });
      pendingNonce = result;
      const messageEl = $("signinMessage");
      if (messageEl) messageEl.textContent = result.message || "";
      const copyButton = $("signinCopyMessage");
      if (copyButton) copyButton.setAttribute("data-portal-copy", result.message || "");
      const expiry = $("signinNonceExpiry");
      if (expiry) expiry.textContent = hasTime(result.expires_at) ? "expires " + timeLabel(result.expires_at) : "";
      return result;
    }

    async function verify(signature) {
      if (!pendingNonce) throw new Error("request a message first");
      await api("/member/v1/auth/verify", {
        method: "POST",
        body: JSON.stringify({
          nonce_id: pendingNonce.nonce_id,
          signature: signature,
          display_name: val("signinDisplayName"),
          contact: val("signinContact")
        })
      });
      window.location.assign("/member");
    }

    on("signinConnect", "click", async () => {
      try {
        if (!window.ethereum) {
          setStatus("No browser wallet was found. Use \"Sign manually instead\" to sign the message with your own signer.", "bad");
          const panel = $("signinManualPanel");
          if (panel) panel.hidden = false;
          return;
        }
        setStatus("Asking your wallet for an address...");
        const accounts = await window.ethereum.request({ method: "eth_requestAccounts" });
        const address = val("signinAddress") || (accounts && accounts[0]) || "";
        if (!address) throw new Error("no address was returned by the wallet");
        const addressEl = $("signinAddress");
        if (addressEl) addressEl.value = address;
        setStatus("Requesting a sign-in message...");
        const nonce = await requestNonce(address);
        setStatus("Waiting for you to sign in your wallet...");
        const signature = await window.ethereum.request({
          method: "personal_sign",
          params: [nonce.message, address]
        });
        setStatus("Verifying the signature...");
        await verify(signature);
      } catch (err) {
        setStatus(err.message || String(err), "bad");
      }
    });

    on("signinManual", "click", () => {
      const panel = $("signinManualPanel");
      if (panel) panel.hidden = false;
    });

    on("signinRequestNonce", "click", async () => {
      try {
        const address = val("signinAddress");
        if (!address) throw new Error("enter the wallet address you will sign with");
        setStatus("Requesting a sign-in message...");
        await requestNonce(address);
        setStatus("Sign the message below with " + address + ", then paste the signature.");
      } catch (err) {
        setStatus(err.message || String(err), "bad");
      }
    });

    on("signinVerify", "click", async () => {
      try {
        const signature = val("signinSignature");
        if (!signature) throw new Error("paste the signature you produced");
        setStatus("Verifying the signature...");
        await verify(signature);
      } catch (err) {
        setStatus(err.message || String(err), "bad");
      }
    });
  }

  // ---------- get started page ----------

  function renderBundleStep(host) {
    const section = $("startBundleSection");
    const target = $("startBundle");
    if (!section || !target) return;
    section.hidden = false;
    const command = "unzip " + host.id + "-pool-bundle.zip -d livepeer-pool && cd livepeer-pool && docker compose up -d";
    target.innerHTML = "";
    target.appendChild(card(
      "<strong>" + esc(hostName(host)) + "</strong>" +
      '<div class="mono">' + esc(host.id) + "</div>" +
      '<p class="small">The bundle is the agent and its configuration. It already carries this host\'s credential, so treat the zip as a secret.</p>' +
      '<div class="actions"><button class="primary-button" data-bundle-download="' + esc(host.id) + '">Download bundle</button></div>' +
      '<p class="small">Then, from the directory you downloaded it to, run this one command:</p>' +
      "<pre>" + esc(command) + "</pre>" +
      '<div class="actions"><button class="copy-button" data-portal-copy="' + esc(command) + '">Copy command</button></div>' +
      '<p class="small">Docker needs the NVIDIA container runtime and access to the Docker socket; the bundle README states what the pool may run on the host. Nothing else is required — no port is opened and no inbound connection is made.</p>'
    ));
    target.querySelectorAll("[data-bundle-download]").forEach((button) => {
      button.onclick = async () => {
        const stored = hostByID(button.getAttribute("data-bundle-download"));
        if (!stored) return;
        try {
          setStatus("Building the bundle...");
          const response = await fetch("/member/v1/enrollments/" + encodeURIComponent(stored.id) + "/bundle", {
            headers: { Authorization: "Bearer " + stored.token }
          });
          if (!response.ok) throw new Error((await response.text()) || ("HTTP " + response.status));
          const blob = await response.blob();
          const url = URL.createObjectURL(blob);
          const link = document.createElement("a");
          link.href = url;
          link.download = stored.id + "-pool-bundle.zip";
          document.body.appendChild(link);
          link.click();
          link.remove();
          window.setTimeout(() => URL.revokeObjectURL(url), 2000);
          setStatus("Bundle downloaded. Unzip it and run the command below.");
        } catch (err) {
          setStatus(err.message || String(err), "bad");
        }
      };
    });
  }

  function initStart() {
    if (!$("startCreate")) return;

    on("startCreate", "click", async () => {
      try {
        const label = val("startHostLabel");
        setStatus("Creating the host enrollment...");
        const result = await api("/member/v1/enrollments", {
          method: "POST",
          body: JSON.stringify({ host_label: label })
        });
        const enrollment = result.enrollment || {};
        const token = result.token || "";
        if (!enrollment.id || !token) throw new Error("the controller did not return an enrollment and token");
        rememberHost(enrollment.id, token, enrollment.host_label || label);
        hosts = loadHosts();
        const section = $("startCredentialSection");
        const target = $("startCredential");
        if (section && target) {
          section.hidden = false;
          target.innerHTML = credentialPanelHTML(enrollment.id, token, "Enrollment token — shown once");
          bindSecretDismiss(target);
        }
        renderBundleStep(hostByID(enrollment.id));
        setStatus("Host enrolled. Save the token, then download the bundle.");
      } catch (err) {
        setStatus(err.message || String(err), "bad");
      }
    });

    on("startAdopt", "click", async () => {
      try {
        const id = val("startAdoptID");
        const token = val("startAdoptToken");
        if (!id || !token) throw new Error("both the enrollment ID and its token are required");
        setStatus("Checking the credential...");
        const status = await api("/member/v1/enrollments/" + encodeURIComponent(id) + "/status", {}, token);
        rememberHost(id, token, status.host_label || "");
        hosts = loadHosts();
        const idEl = $("startAdoptID");
        const tokenEl = $("startAdoptToken");
        if (idEl) idEl.value = "";
        if (tokenEl) tokenEl.value = "";
        setStatus("Added " + (status.host_label || id) + " to this browser.");
      } catch (err) {
        setStatus(err.status === 401 ? "That enrollment ID and token are not a valid pair, or the host has been revoked or retired." : (err.message || String(err)), "bad");
      }
    });

    // A member who reloads this page mid-setup still needs the command; the
    // token panel is deliberately not re-shown, because it cannot be re-read.
    if (hosts.length) renderBundleStep(hosts[hosts.length - 1]);
  }

  // ---------- status loading ----------

  async function loadStatuses() {
    hosts = loadHosts();
    hostStatuses = await Promise.all(hosts.map(async (host) => {
      try {
        const status = await api("/member/v1/enrollments/" + encodeURIComponent(host.id) + "/status", {}, host.token);
        return { host: host, status: status, error: "" };
      } catch (err) {
        return {
          host: host,
          status: null,
          error: err.status === 401
            ? "This browser's credential for the host is no longer accepted. The agent rotates its own enrollment token on a cadence (daily by default), so a copy kept here goes stale by design; it can also have been revoked by the operator. Read the current token from the host's enrollment-token file and add it again."
            : (err.message || String(err))
        };
      }
    }));
    return hostStatuses;
  }

  function firstUsableHost() {
    const entry = hostStatuses.find((item) => item.status);
    return entry ? entry.host : (hosts.length ? hosts[0] : null);
  }

  // ---------- hosts page ----------

  // The controller reports the host's last check-in as last_seen_at. Nothing
  // writes it today, so an empty value is stated as "not recorded" rather than
  // rendered as a dead agent — claiming a host is down on the strength of a
  // field nobody fills would be worse than saying nothing.
  function agentLine(status) {
    if (!hasTime(status.last_seen_at)) {
      return '<span class="pill">agent check-in not recorded</span>';
    }
    const minutes = (Date.now() - Date.parse(status.last_seen_at)) / 60000;
    const tone = minutes < 5 ? "ok" : (minutes < 30 ? "warn" : "bad");
    const wording = minutes < 5 ? "agent reporting" : (minutes < 30 ? "agent quiet" : "agent not reporting");
    return '<span class="pill pill-' + tone + '">' + wording + " · last seen " + esc(agoLabel(status.last_seen_at)) + "</span>";
  }

  function reasonHTML(placement) {
    const code = String(placement.reason_code || "").trim();
    const evidence = String(placement.evidence || "").trim();
    if (!code && !evidence) {
      return '<dl class="reason">' +
        "<dt>Reason</dt>" +
        '<dd class="muted">No ladder transition has been recorded for this placement yet — it is where it started, not somewhere it was moved to.</dd>' +
        "</dl>";
    }
    return '<dl class="reason">' +
      "<dt>Reason</dt>" +
      '<dd class="mono">' + (code ? esc(code) : '<span class="muted">not recorded</span>') + "</dd>" +
      "<dt>Evidence</dt>" +
      "<dd>" + (evidence ? esc(evidence) : '<span class="muted">none recorded</span>') + "</dd>" +
      "</dl>";
  }

  function placementHTML(placement) {
    const share = sharePPMLabel(placement.share_ppm);
    return '<div class="placement-item state-' + esc(String(placement.state || "unknown").toLowerCase()) + '">' +
      '<div class="row">' +
        statePill(placement.state) +
        '<span class="pill">' + esc(placement.role || "primary") + "</span>" +
        (share ? '<span class="pill">' + esc(share) + "</span>" : "") +
      "</div>" +
      '<div class="mono">template ' + esc(placement.template_id || "unknown") + "</div>" +
      '<div class="small">in this state since ' + esc(timeLabel(placement.since_at)) + "</div>" +
      reasonHTML(placement) +
      "</div>";
  }

  function gpuHTML(gpu) {
    const placements = gpu.placements || [];
    return '<div class="gpu-item">' +
      "<strong>" + esc(gpu.gpu_model || gpu.hardware_unit_id) + "</strong>" +
      '<div class="row">' + statePill(gpu.state) + '<span class="pill">' + esc(gpu.hardware_unit_id) + "</span></div>" +
      (placements.length
        ? '<div class="placement-list">' + placements.map(placementHTML).join("") + "</div>"
        : '<p class="small muted">No template is placed on this GPU yet. Placement runs from the hardware the agent reported, so a GPU that has just attached waits for the next pass, and one that matches no enabled template waits for the operator to enable one that fits it.</p>') +
      "</div>";
  }

  function renderHosts() {
    renderCards("memberHosts", hostStatuses, (entry) => {
      if (!entry.status) {
        return "<strong>" + esc(hostName(entry.host)) + "</strong>" +
          '<div class="mono">' + esc(entry.host.id) + "</div>" +
          '<div class="row"><span class="pill pill-bad">unreadable</span></div>' +
          '<p class="small">' + esc(entry.error) + "</p>" +
          '<p class="small muted">The host itself always has the current token, in the <code>enrollment-token</code> file beside its compose file. Add it again from <a href="/member" class="inline-link">Get started</a>.</p>';
      }
      const status = entry.status;
      const gpus = status.gpus || [];
      return "<strong>" + esc(status.host_label || entry.host.label || status.enrollment_id) + "</strong>" +
        '<div class="row">' + statePill(status.status) + agentLine(status) + "</div>" +
        '<div class="mono">' + esc(status.enrollment_id) + "</div>" +
        (gpus.length
          ? '<div class="gpu-list">' + gpus.map(gpuHTML).join("") + "</div>"
          : '<p class="small muted">This host has not reported a GPU yet. The agent reports its hardware when it attaches, so a host that has just been started appears here within a poll.</p>');
    }, '<strong>No hosts on this browser</strong><span class="muted">Enrol one from <a href="/member" class="inline-link">Get started</a>, or add an existing host\'s credential back.</span>');
  }

  // ---------- earnings page ----------

  function earningsRowHTML(entry) {
    return "<strong>" + esc(entry.settlement_window_id || "unattributed window") + "</strong>" +
      '<div class="row">' + statePill(entry.status) + "</div>" +
      '<div class="metric-value">' + esc(ethLabel(entry.amount_wei)) + "</div>" +
      '<div class="mono">' + esc(weiLabel(entry.amount_wei)) + "</div>" +
      '<div class="small">' + esc(timeLabel(entry.at)) + "</div>";
  }

  async function renderEarnings() {
    if (!$("earningsWindows")) return;
    const host = firstUsableHost();
    if (!host) {
      renderCards("earningsWindows", [], earningsRowHTML, '<strong>No hosts on this browser</strong><span class="muted">Earnings are read with a host credential. Enrol a host, or add an existing one back from <a href="/member" class="inline-link">Get started</a>.</span>');
      renderCards("earningsHistory", [], earningsRowHTML, '<span class="muted">Nothing to show yet.</span>');
      return;
    }
    const earnings = await api("/member/v1/enrollments/" + encodeURIComponent(host.id) + "/earnings", {}, host.token);
    const set = (id, text) => { const el = $(id); if (el) el.textContent = text; };
    set("earningsPaidEth", ethLabel(earnings.total_paid_wei));
    set("earningsPaidWei", weiLabel(earnings.total_paid_wei));
    set("earningsPendingEth", ethLabel(earnings.pending_wei));
    set("earningsPendingWei", weiLabel(earnings.pending_wei));
    const windows = (earnings.windows || []).slice().reverse();
    set("earningsWindowCount", windows.length);
    renderCards("earningsWindows", windows, earningsRowHTML,
      '<strong>Nothing attributed yet</strong><span class="muted">A line appears here once a settlement window that included your work has been closed and materialised into a batch.</span>');
    renderCards("earningsHistory", windows.filter((entry) => String(entry.status || "").toLowerCase() === "paid"), earningsRowHTML,
      '<span class="muted">No payment has been sent yet.</span>');
  }

  // ---------- settings page ----------

  function ownGPUs() {
    const out = [];
    hostStatuses.forEach((entry) => {
      if (!entry.status) return;
      (entry.status.gpus || []).forEach((gpu) => {
        out.push({
          id: gpu.hardware_unit_id,
          label: (gpu.gpu_model || gpu.hardware_unit_id) + " on " + (entry.status.host_label || entry.status.enrollment_id)
        });
      });
    });
    return out;
  }

  // Suggestions come from the templates already placed on this member's own
  // GPUs. The pool's full catalog is an operator surface, so it is not read
  // here; any catalog ID typed by hand is still accepted, and one the catalog
  // does not carry is rejected by the API rather than stored.
  function ownTemplateIDs() {
    const seen = new Set();
    hostStatuses.forEach((entry) => {
      if (!entry.status) return;
      (entry.status.gpus || []).forEach((gpu) => {
        (gpu.placements || []).forEach((placement) => {
          if (placement.template_id) seen.add(placement.template_id);
        });
      });
    });
    return Array.from(seen).sort();
  }

  function renderOptOutInputs() {
    const list = $("optOutTemplateOptions");
    if (list) {
      list.innerHTML = ownTemplateIDs().map((id) => '<option value="' + esc(id) + '"></option>').join("");
    }
    const select = $("optOutHardwareUnitID");
    if (select) {
      select.innerHTML = '<option value="">Every GPU on my address</option>' +
        ownGPUs().map((gpu) => '<option value="' + esc(gpu.id) + '">' + esc(gpu.label) + "</option>").join("");
    }
  }

  function renderOptOuts(items) {
    renderCards("settingsOptOuts", items, (item) =>
      "<strong>" + esc(item.template_id) + "</strong>" +
      '<div class="row"><span class="pill">' + (item.hardware_unit_id ? "one GPU" : "every GPU") + "</span>" +
      (item.hardware_unit_id ? '<span class="pill">' + esc(item.hardware_unit_id) + "</span>" : "") + "</div>" +
      (item.reason ? '<div class="small">' + esc(item.reason) + "</div>" : "") +
      '<div class="small muted">declined ' + esc(timeLabel(item.created_at)) + "</div>" +
      '<div class="actions"><button class="secondary" data-optout-withdraw="' + esc(item.id) + '">Withdraw</button></div>',
      '<span class="muted">You have not declined any template. Every enabled template your GPUs qualify for may be placed on them.</span>');
    const target = $("settingsOptOuts");
    if (!target) return;
    target.querySelectorAll("[data-optout-withdraw]").forEach((button) => {
      button.onclick = () => confirmInline(button, {
        title: "Withdraw this refusal?",
        bodyHTML: "Placement may put this template back on your GPUs on its next pass, and the agent will pull and start its runner.",
        confirmLabel: "Withdraw refusal",
        run: async () => {
          const host = firstUsableHost();
          if (!host) return;
          try {
            setStatus("Withdrawing the refusal...");
            await api("/member/v1/enrollments/" + encodeURIComponent(host.id) + "/opt-outs/" + encodeURIComponent(button.getAttribute("data-optout-withdraw")), { method: "DELETE" }, host.token);
            await refreshAll();
            setStatus("Refusal withdrawn.");
          } catch (err) {
            setStatus(err.message || String(err), "bad");
          }
        }
      });
    });
  }

  function renderCredentials() {
    renderCards("settingsCredentials", hostStatuses, (entry) =>
      "<strong>" + esc(entry.status ? (entry.status.host_label || entry.status.enrollment_id) : hostName(entry.host)) + "</strong>" +
      '<div class="mono">' + esc(entry.host.id) + "</div>" +
      (entry.status ? '<div class="row">' + statePill(entry.status.status) + "</div>" : '<div class="row"><span class="pill pill-bad">unreadable</span></div>') +
      '<div class="actions"><button class="secondary" data-rotate="' + esc(entry.host.id) + '">Rotate credential</button></div>',
      '<span class="muted">No hosts on this browser.</span>');
    const target = $("settingsCredentials");
    if (!target) return;
    target.querySelectorAll("[data-rotate]").forEach((button) => {
      const host = hostByID(button.getAttribute("data-rotate"));
      if (!host) return;
      button.onclick = () => confirmInline(button, {
        title: "Rotate the credential for " + hostName(host) + "?",
        bodyHTML: "The new token is shown <strong>once</strong>. The controller keeps only a hash of it and can never show it again — if you lose it, the only copy left is the one on the host itself.<br><br>The current token stops working the moment this completes, so the running agent will fail to attach until the new one is written to its <code>enrollment-token</code> file and it is restarted. The agent already rotates its own credential on a cadence, so rotate here only when you have reason to think the token leaked — not as routine maintenance.",
        phrase: "ROTATE",
        confirmLabel: "Rotate and show the new token once",
        run: async () => {
          try {
            setStatus("Rotating the credential...");
            const result = await api("/member/v1/enrollments/" + encodeURIComponent(host.id) + "/rotate", { method: "POST", body: "{}" }, host.token);
            const token = result.enrollment_token || "";
            if (!token) throw new Error("the controller did not return a new token");
            rememberHost(host.id, token, host.label);
            hosts = loadHosts();
            const scope = button.closest(".card");
            if (scope) {
              const panel = document.createElement("div");
              panel.innerHTML = credentialPanelHTML(host.id, token, "New enrollment token — shown once");
              scope.appendChild(panel);
              bindSecretDismiss(panel);
            }
            setStatus("Credential rotated. Write the new token into the host's enrollment-token file and restart the agent, or it will not be able to attach.");
          } catch (err) {
            setStatus(err.message || String(err), "bad");
          }
        }
      });
    });
  }

  function renderRetire() {
    renderCards("settingsRetire", hostStatuses, (entry) => {
      const placements = entry.status
        ? (entry.status.gpus || []).reduce((total, gpu) => total + (gpu.placements || []).length, 0)
        : 0;
      return "<strong>" + esc(entry.status ? (entry.status.host_label || entry.status.enrollment_id) : hostName(entry.host)) + "</strong>" +
        '<div class="mono">' + esc(entry.host.id) + "</div>" +
        '<div class="row"><span class="pill">' + placements + " placement(s)</span></div>" +
        '<div class="actions"><button class="secondary" data-retire="' + esc(entry.host.id) + '">Retire this host</button></div>';
    }, '<span class="muted">No hosts on this browser.</span>');
    const target = $("settingsRetire");
    if (!target) return;
    target.querySelectorAll("[data-retire]").forEach((button) => {
      const host = hostByID(button.getAttribute("data-retire"));
      if (!host) return;
      button.onclick = () => confirmInline(button, {
        title: "Retire " + hostName(host) + "?",
        bodyHTML: "Every placement on this host is set to <strong>draining</strong>. Draining is not stopping: the broker stops sending new work, and the runners keep serving the requests already dispatched to them until those finish. Only then does the host go quiet, and only then is the placement retired.<br><br>Leave the host running until it does. Killing it now abandons work that is already in flight, and that counts against you.",
        phrase: hostName(host),
        confirmLabel: "Retire and start draining",
        run: async () => {
          try {
            setStatus("Retiring the host...");
            const result = await api("/member/v1/enrollments/" + encodeURIComponent(host.id) + "/retire", { method: "POST", body: "{}" }, host.token);
            await refreshAll();
            setStatus("Retiring " + hostName(host) + ": " + (result.draining_placements || 0) + " placement(s) are draining. Leave the host running until they finish.");
          } catch (err) {
            setStatus(err.message || String(err), "bad");
          }
        }
      });
    });
  }

  function renderLocalHosts() {
    renderCards("settingsLocal", hosts, (host) =>
      "<strong>" + esc(hostName(host)) + "</strong>" +
      '<div class="mono">' + esc(host.id) + "</div>" +
      '<div class="small muted">added ' + esc(timeLabel(host.added_at)) + "</div>" +
      '<div class="actions"><button class="secondary" data-forget="' + esc(host.id) + '">Forget on this browser</button></div>',
      '<span class="muted">This browser holds no host credentials.</span>');
    const target = $("settingsLocal");
    if (!target) return;
    target.querySelectorAll("[data-forget]").forEach((button) => {
      button.onclick = () => confirmInline(button, {
        title: "Forget this host on this browser?",
        bodyHTML: "The host keeps running and keeps earning. Only this browser's copy of its token goes. The host still has the current one in its <code>enrollment-token</code> file, so you can add it back from there; no one, including the operator, can print it for you.",
        confirmLabel: "Forget it here",
        run: async () => {
          forgetHost(button.getAttribute("data-forget"));
          await refreshAll();
          setStatus("Forgotten on this browser.");
        }
      });
    });
  }

  async function renderSettings() {
    if (!$("settingsOptOuts")) return;
    renderOptOutInputs();
    renderCredentials();
    renderRetire();
    renderLocalHosts();
    const host = firstUsableHost();
    if (!host) {
      renderOptOuts([]);
      return;
    }
    const optOuts = await api("/member/v1/enrollments/" + encodeURIComponent(host.id) + "/opt-outs", {}, host.token);
    renderOptOuts(optOuts.opt_outs || []);
  }

  function initSettings() {
    on("optOutCreate", "click", async () => {
      const host = firstUsableHost();
      if (!host) {
        setStatus("A readable host credential is needed to record a refusal.", "bad");
        return;
      }
      try {
        const templateID = val("optOutTemplateID");
        if (!templateID) throw new Error("a template ID is required");
        const select = $("optOutHardwareUnitID");
        setStatus("Recording the refusal...");
        await api("/member/v1/enrollments/" + encodeURIComponent(host.id) + "/opt-outs", {
          method: "POST",
          body: JSON.stringify({
            template_id: templateID,
            hardware_unit_id: select ? select.value : "",
            reason: val("optOutReason")
          })
        }, host.token);
        const templateInput = $("optOutTemplateID");
        const reasonInput = $("optOutReason");
        if (templateInput) templateInput.value = "";
        if (reasonInput) reasonInput.value = "";
        await refreshAll();
        setStatus("Refusal recorded. Placement stops offering that template to the GPUs it covers, and any running placement of it drains.");
      } catch (err) {
        setStatus(err.message || String(err), "bad");
      }
    });

    on("settingsForgetAll", "click", (event) => {
      confirmInline(event.currentTarget, {
        title: "Forget every host on this browser?",
        bodyHTML: "Your hosts keep running and keep earning. This browser loses every stored token, and the portal cannot show any host until they are pasted back. Each host has its current token in its <code>enrollment-token</code> file; the controller keeps only hashes and cannot reissue one.",
        confirmLabel: "Forget all of them",
        run: async () => {
          saveHosts([]);
          await refreshAll();
          setStatus("All host credentials cleared from this browser.");
        }
      });
    });
  }

  // ---------- shell ----------

  async function refreshAll() {
    if (!$("status")) return;
    try {
      setStatus("Reading your hosts...");
      await loadStatuses();
      renderHosts();
      await renderEarnings();
      await renderSettings();
      setStatus(hosts.length ? "Up to date." : "This browser holds no host credentials yet.");
    } catch (err) {
      setStatus(err.message || String(err), "bad");
    }
  }

  function initShell() {
    const logout = document.querySelector("[data-member-logout]");
    if (logout) {
      logout.addEventListener("submit", async (event) => {
        event.preventDefault();
        try {
          await fetch("/member/logout", { method: "POST" });
        } catch (_) {}
        window.location.assign("/member/signin");
      });
    }
    on("refresh", "click", () => { void refreshAll(); });
  }

  hosts = loadHosts();
  initSignIn();
  initShell();
  initStart();
  initSettings();
  if ($("status")) void refreshAll();
})();
