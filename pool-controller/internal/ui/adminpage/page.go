package adminpage

func HTML() []byte {
	return []byte(page)
}

const page = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Pool Control Plane</title>
  <style>
    :root {
      --bg: #f3efe6;
      --panel: #fffaf2;
      --ink: #1f2430;
      --muted: #5d6777;
      --line: #d5c7af;
      --accent: #9b3d23;
      --accent-soft: #f2d7c6;
      --ok: #2f6f4f;
      --warn: #8c5d09;
      --bad: #9b243b;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: Georgia, "Iowan Old Style", serif;
      background:
        radial-gradient(circle at top left, rgba(155,61,35,0.14), transparent 28%),
        linear-gradient(180deg, #f9f4ea 0%, var(--bg) 100%);
      color: var(--ink);
    }
    .shell {
      max-width: 1280px;
      margin: 0 auto;
      padding: 24px;
    }
    .hero {
      display: grid;
      gap: 12px;
      margin-bottom: 22px;
      padding: 24px;
      border: 1px solid var(--line);
      background: linear-gradient(135deg, rgba(155,61,35,0.08), rgba(255,250,242,0.98));
    }
    .eyebrow {
      font-size: 12px;
      letter-spacing: 0.12em;
      text-transform: uppercase;
      color: var(--muted);
    }
    h1 {
      margin: 0;
      font-size: clamp(28px, 4vw, 48px);
      line-height: 0.95;
    }
    .lede {
      margin: 0;
      color: var(--muted);
      max-width: 70ch;
    }
    .toolbar {
      display: flex;
      flex-wrap: wrap;
      gap: 10px;
      align-items: end;
      margin-bottom: 22px;
      padding: 16px;
      border: 1px solid var(--line);
      background: var(--panel);
    }
    label {
      display: grid;
      gap: 6px;
      font-size: 13px;
      color: var(--muted);
    }
    input, textarea, select, button {
      font: inherit;
    }
    input, textarea, select {
      width: 100%;
      padding: 10px 12px;
      border: 1px solid var(--line);
      background: #fff;
      color: var(--ink);
    }
    textarea { min-height: 120px; resize: vertical; }
    button {
      border: 1px solid var(--accent);
      background: var(--accent);
      color: #fffaf2;
      padding: 10px 14px;
      cursor: pointer;
    }
    button.secondary {
      background: transparent;
      color: var(--accent);
    }
    .status {
      min-height: 22px;
      font-size: 14px;
      color: var(--muted);
      margin-bottom: 18px;
    }
    .grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
      gap: 18px;
    }
    .panel {
      border: 1px solid var(--line);
      background: var(--panel);
      padding: 16px;
      display: grid;
      gap: 12px;
    }
    .panel h2 {
      margin: 0;
      font-size: 20px;
    }
    .card-list {
      display: grid;
      gap: 10px;
    }
    .card {
      border: 1px solid var(--line);
      background: #fff;
      padding: 12px;
      display: grid;
      gap: 8px;
    }
    .card strong { font-size: 16px; }
    .row {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
      align-items: center;
    }
    .pill {
      display: inline-block;
      padding: 2px 8px;
      border: 1px solid var(--line);
      font-size: 12px;
      color: var(--muted);
    }
    .mono {
      font-family: "SFMono-Regular", Consolas, monospace;
      font-size: 12px;
      word-break: break-all;
    }
    pre {
      margin: 0;
      overflow: auto;
      padding: 12px;
      background: #fff;
      border: 1px solid var(--line);
      font-size: 12px;
    }
    .ok { color: var(--ok); }
    .warn { color: var(--warn); }
    .bad { color: var(--bad); }
    .small { font-size: 12px; color: var(--muted); }
  </style>
</head>
<body>
  <div class="shell">
    <section class="hero">
      <div class="eyebrow">Pool Control Plane</div>
      <h1>Offers, Members, Assignments, Runtime</h1>
      <p class="lede">This operator page works directly against the new control-plane APIs. It assumes nothing is published until an approved backend is assigned to an orch-owned offer.</p>
    </section>

    <section class="toolbar">
      <label style="flex:1 1 280px">
        Admin Token
        <input id="token" type="password" placeholder="Bearer token for /admin/v1/*">
      </label>
      <button id="refresh">Refresh</button>
      <button id="markStarted" class="secondary">Mark Apply Started</button>
      <button id="markFailed" class="secondary">Mark Apply Failed</button>
      <button id="markApplied" class="secondary">Mark Desired Revision Applied</button>
    </section>

    <div id="status" class="status"></div>

    <section class="grid">
      <div class="panel">
        <h2>Create Offer</h2>
        <label>Offer JSON
          <textarea id="offerPayload">{
  "id": "rerank-zerank2",
  "capability_id": "rerank",
  "offering_id": "zerank-2-default",
  "interaction_mode": "http-reqresp@v0",
  "work_unit": {
    "name": "requests",
    "extractor": {
      "type": "request-formula",
      "expression": "1"
    }
  },
  "price": {
    "amount_wei": "372000000000",
    "per_units": 1
  }
}</textarea>
        </label>
        <button id="createOffer">Create Offer</button>
      </div>

      <div class="panel">
        <h2>Submit Join Request</h2>
        <label>Join Request JSON
          <textarea id="joinPayload">{
  "id": "join-sample",
  "member_eth_address": "0xmember",
  "display_name": "member-a",
  "payout_mode": "onchain",
  "requested_backends": [
    {
      "id": "backend-sample",
      "transport": "http",
      "url": "http://backend:8080/v1/rerank",
      "auth": { "method": "none" },
      "health_probe": {
        "type": "http-status",
        "config": { "url": "http://backend:8080/healthz" }
      },
      "claimed_capabilities": [
        {
          "capability_id": "rerank",
          "offering_id": "zerank-2-default",
          "interaction_mode": "http-reqresp@v0"
        }
      ]
    }
  ]
}</textarea>
        </label>
        <button id="submitJoin">Submit Join Request</button>
      </div>

      <div class="panel">
        <h2>Create Assignment</h2>
        <label>Assignment JSON
          <textarea id="assignmentPayload">{
  "id": "assign-sample",
  "offer_id": "rerank-zerank2",
  "member_backend_id": "backend-sample"
}</textarea>
        </label>
        <button id="createAssignment">Create Assignment</button>
      </div>
    </section>

    <section class="grid" style="margin-top:18px">
      <div class="panel">
        <h2>Offers</h2>
        <div id="offers" class="card-list"></div>
      </div>
      <div class="panel">
        <h2>Join Requests</h2>
        <div id="joinRequests" class="card-list"></div>
      </div>
      <div class="panel">
        <h2>Members</h2>
        <div id="members" class="card-list"></div>
      </div>
      <div class="panel">
        <h2>Backends</h2>
        <div id="backends" class="card-list"></div>
      </div>
      <div class="panel">
        <h2>Assignments</h2>
        <div id="assignments" class="card-list"></div>
      </div>
      <div class="panel">
        <h2>Broker Runtime</h2>
        <div id="runtime" class="card-list"></div>
        <pre id="runtimeYaml"></pre>
      </div>
    </section>
  </div>

  <script>
    const $ = (id) => document.getElementById(id);
    const statusEl = $("status");

    function tokenHeaders(includeJSON = true) {
      const headers = {};
      const token = $("token").value.trim();
      if (token) headers["Authorization"] = "Bearer " + token;
      if (includeJSON) headers["Content-Type"] = "application/json";
      return headers;
    }

    function setStatus(msg, cls = "") {
      statusEl.className = "status " + cls;
      statusEl.textContent = msg;
    }

    async function api(path, opts = {}) {
      const response = await fetch(path, {
        ...opts,
        headers: {
          ...tokenHeaders(opts.body !== undefined),
          ...(opts.headers || {})
        }
      });
      if (!response.ok) {
        const text = await response.text();
        throw new Error(text || ("HTTP " + response.status));
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

    async function refreshAll() {
      setStatus("Refreshing control-plane state...");
      try {
        const [offers, joinRequests, members, backends, assignments, runtime, brokerConfig] = await Promise.all([
          api("/admin/v1/offers"),
          api("/admin/v1/join-requests"),
          api("/admin/v1/members"),
          api("/admin/v1/member-backends"),
          api("/admin/v1/assignments"),
          api("/admin/v1/broker-runtime"),
          api("/admin/v1/broker-config", { headers: tokenHeaders(false) })
        ]);

        renderOffers(offers.offers || []);
        renderJoinRequests(joinRequests.join_requests || []);
        renderMembers(members.members || []);
        renderBackends(backends.backends || []);
        renderAssignments(assignments.assignments || []);
        renderRuntime(runtime, brokerConfig);
        setStatus("Control-plane state refreshed.", "ok");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    }

    function renderOffers(items) {
      const host = $("offers");
      host.innerHTML = "";
      items.forEach(item => {
        const el = card(
          "<strong>" + item.id + "</strong>" +
          '<div class="row"><span class="pill">' + item.capability_id + '</span><span class="pill">' + item.offering_id + '</span><span class="pill">' + item.status + '</span></div>' +
          '<div class="small">mode: ' + item.interaction_mode + '</div>' +
          '<div class="mono">price: ' + item.price.amount_wei + " / " + item.price.per_units + '</div>' +
          '<div class="row">' +
            '<button data-offer-active="' + item.id + '" class="secondary">Set Active</button>' +
            '<button data-offer-disabled="' + item.id + '" class="secondary">Disable</button>' +
          '</div>'
        );
        host.appendChild(el);
      });
      host.querySelectorAll("[data-offer-active]").forEach(btn => btn.onclick = () => patchOfferStatus(btn.dataset.offerActive, "active"));
      host.querySelectorAll("[data-offer-disabled]").forEach(btn => btn.onclick = () => patchOfferStatus(btn.dataset.offerDisabled, "disabled"));
    }

    function renderJoinRequests(items) {
      const host = $("joinRequests");
      host.innerHTML = "";
      items.forEach(item => {
        const el = card(
          "<strong>" + item.id + "</strong>" +
          '<div class="row"><span class="pill">' + item.status + '</span><span class="pill">' + (item.payout_mode || "onchain") + '</span></div>' +
          '<div class="mono">' + item.member_eth_address + '</div>' +
          '<div class="small">backends: ' + (item.requested_backends || []).length + '</div>' +
          ((item.requested_backends || []).map(b => '<div class="small">backend ' + b.id + ': ' + (b.verification_status || "unknown") + (b.verification_error ? " (" + b.verification_error + ")" : "") + '</div>').join("")) +
          '<div class="row">' +
            '<button data-refresh-join="' + item.id + '" class="secondary">Refresh Verification</button>' +
            '<button data-approve="' + item.id + '">Approve</button>' +
            '<button data-reject="' + item.id + '" class="secondary">Reject</button>' +
          '</div>'
        );
        host.appendChild(el);
      });
      host.querySelectorAll("[data-refresh-join]").forEach(btn => btn.onclick = () => refreshJoin(btn.dataset.refreshJoin));
      host.querySelectorAll("[data-approve]").forEach(btn => btn.onclick = () => reviewJoin(btn.dataset.approve, "approve"));
      host.querySelectorAll("[data-reject]").forEach(btn => btn.onclick = () => reviewJoin(btn.dataset.reject, "reject"));
    }

    function renderMembers(items) {
      const host = $("members");
      host.innerHTML = "";
      items.forEach(item => {
        const el = card(
          "<strong>" + (item.display_name || item.eth_address) + '</strong>' +
          '<div class="row"><span class="pill">' + (item.status || "active") + '</span></div>' +
          '<div class="mono">' + item.eth_address + '</div>' +
          '<div class="small">payout: ' + (item.payout_mode || "onchain") + '</div>' +
          '<div class="row">' +
            '<button data-member-active="' + item.id + '" class="secondary">Set Active</button>' +
            '<button data-member-suspended="' + item.id + '" class="secondary">Suspend</button>' +
          '</div>'
        );
        host.appendChild(el);
      });
      host.querySelectorAll("[data-member-active]").forEach(btn => btn.onclick = () => patchMember(btn.dataset.memberActive, "active"));
      host.querySelectorAll("[data-member-suspended]").forEach(btn => btn.onclick = () => patchMember(btn.dataset.memberSuspended, "suspended"));
    }

    function renderBackends(items) {
      const host = $("backends");
      host.innerHTML = "";
      items.forEach(item => {
        const el = card(
          "<strong>" + item.id + '</strong>' +
          '<div class="row"><span class="pill">' + item.status + '</span><span class="pill">' + item.verification_status + '</span></div>' +
          '<div class="mono">' + item.url + '</div>' +
          '<div class="small">' + (item.verification_error || "") + '</div>' +
          '<div class="row">' +
            '<button data-backend-verify="' + item.id + '" class="secondary">Verify</button>' +
            '<button data-backend-active="' + item.id + '" class="secondary">Set Active</button>' +
            '<button data-backend-draining="' + item.id + '" class="secondary">Drain</button>' +
            '<button data-backend-disabled="' + item.id + '" class="secondary">Disable</button>' +
          '</div>'
        );
        host.appendChild(el);
      });
      host.querySelectorAll("[data-backend-verify]").forEach(btn => btn.onclick = () => verifyBackend(btn.dataset.backendVerify));
      host.querySelectorAll("[data-backend-active]").forEach(btn => btn.onclick = () => patchBackend(btn.dataset.backendActive, "active"));
      host.querySelectorAll("[data-backend-draining]").forEach(btn => btn.onclick = () => patchBackend(btn.dataset.backendDraining, "draining"));
      host.querySelectorAll("[data-backend-disabled]").forEach(btn => btn.onclick = () => patchBackend(btn.dataset.backendDisabled, "disabled"));
    }

    function renderAssignments(items) {
      const host = $("assignments");
      host.innerHTML = "";
      items.forEach(item => {
        const el = card(
          "<strong>" + item.id + '</strong>' +
          '<div class="row"><span class="pill">' + item.status + '</span></div>' +
          '<div class="small">offer: ' + item.offer_id + '</div>' +
          '<div class="small">backend: ' + item.member_backend_id + '</div>' +
          '<div class="row">' +
            '<button data-assignment-active="' + item.id + '" class="secondary">Set Active</button>' +
            '<button data-assignment-draining="' + item.id + '" class="secondary">Drain</button>' +
            '<button data-assignment-disabled="' + item.id + '" class="secondary">Disable</button>' +
            '<button data-delete-assignment="' + item.id + '" class="secondary">Delete</button>' +
          '</div>'
        );
        host.appendChild(el);
      });
      host.querySelectorAll("[data-assignment-active]").forEach(btn => btn.onclick = () => patchAssignment(btn.dataset.assignmentActive, "active"));
      host.querySelectorAll("[data-assignment-draining]").forEach(btn => btn.onclick = () => patchAssignment(btn.dataset.assignmentDraining, "draining"));
      host.querySelectorAll("[data-assignment-disabled]").forEach(btn => btn.onclick = () => patchAssignment(btn.dataset.assignmentDisabled, "disabled"));
      host.querySelectorAll("[data-delete-assignment]").forEach(btn => btn.onclick = () => deleteAssignment(btn.dataset.deleteAssignment));
    }

    function renderRuntime(item, yaml) {
      const host = $("runtime");
      host.innerHTML = "";
      host.appendChild(card(
        "<strong>Desired vs Applied</strong>" +
        '<div class="row"><span class="pill">' + (item.dirty ? "dirty" : "converged") + '</span><span class="pill">' + (item.last_apply_status || "unapplied") + '</span></div>' +
        '<div class="small">desired: <span class="mono">' + (item.desired_revision || "") + '</span></div>' +
        '<div class="small">applied: <span class="mono">' + (item.applied_revision || "") + '</span></div>' +
        '<div class="small">offers: ' + item.offer_count + ', members: ' + item.member_count + ', backends: ' + item.backend_count + ', assignments: ' + item.assignment_count + '</div>'
      ));
      $("runtimeYaml").textContent = yaml;
    }

    async function submitJSON(path, payload, method = "POST") {
      await api(path, { method, body: payload });
      await refreshAll();
    }

    async function reviewJoin(id, action) {
      try {
        setStatus("Submitting join-request review...");
        await submitJSON("/admin/v1/join-requests/" + id + "/" + action, "{}");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    }

    async function refreshJoin(id) {
      try {
        setStatus("Refreshing join-request verification...");
        await submitJSON("/admin/v1/join-requests/" + id + "/refresh", "{}");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    }

    async function patchBackend(id, status) {
      try {
        setStatus("Updating backend status...");
        await submitJSON("/admin/v1/member-backends/" + id, JSON.stringify({ status }), "PATCH");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    }

    async function verifyBackend(id) {
      try {
        setStatus("Verifying backend...");
        await submitJSON("/admin/v1/member-backends/" + id + "/verify", "{}");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    }

    async function patchMember(id, status) {
      try {
        setStatus("Updating member status...");
        await submitJSON("/admin/v1/members/" + id, JSON.stringify({ status }), "PATCH");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    }

    async function patchOfferStatus(id, status) {
      try {
        setStatus("Updating offer status...");
        await submitJSON("/admin/v1/offers/" + id, JSON.stringify({ status }), "PATCH");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    }

    async function deleteAssignment(id) {
      try {
        setStatus("Deleting assignment...");
        await api("/admin/v1/assignments/" + id, { method: "DELETE", headers: tokenHeaders(false) });
        await refreshAll();
      } catch (err) {
        setStatus(err.message, "bad");
      }
    }

    async function patchAssignment(id, status) {
      try {
        setStatus("Updating assignment status...");
        await submitJSON("/admin/v1/assignments/" + id, JSON.stringify({ status }), "PATCH");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    }

    $("refresh").onclick = refreshAll;
    $("createOffer").onclick = async () => {
      try {
        setStatus("Creating offer...");
        await submitJSON("/admin/v1/offers", $("offerPayload").value);
      } catch (err) {
        setStatus(err.message, "bad");
      }
    };
    $("submitJoin").onclick = async () => {
      try {
        setStatus("Submitting join request...");
        await api("/member/v1/join-requests", {
          method: "POST",
          body: $("joinPayload").value,
          headers: { "Content-Type": "application/json" }
        });
        await refreshAll();
      } catch (err) {
        setStatus(err.message, "bad");
      }
    };
    $("createAssignment").onclick = async () => {
      try {
        setStatus("Creating assignment...");
        await submitJSON("/admin/v1/assignments", $("assignmentPayload").value);
      } catch (err) {
        setStatus(err.message, "bad");
      }
    };
    $("markApplied").onclick = async () => {
      try {
        setStatus("Marking desired revision applied...");
        await submitJSON("/admin/v1/broker-runtime/mark-applied", "{}");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    };
    $("markStarted").onclick = async () => {
      try {
        setStatus("Marking apply started...");
        await submitJSON("/admin/v1/broker-runtime/mark-started", "{}");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    };
    $("markFailed").onclick = async () => {
      try {
        const error = window.prompt("Apply failure reason", "reload failed") || "";
        setStatus("Marking apply failed...");
        await submitJSON("/admin/v1/broker-runtime/mark-failed", JSON.stringify({ error }));
      } catch (err) {
        setStatus(err.message, "bad");
      }
    };

    refreshAll();
  </script>
</body>
</html>`
