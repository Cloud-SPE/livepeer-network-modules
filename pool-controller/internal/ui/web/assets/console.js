    const $ = (id) => document.getElementById(id);
    const on = (id, ev, fn) => { const el = $(id); if (el) el.addEventListener(ev, fn); };
    const val = (id) => { const el = $(id); return el ? el.value.trim() : ""; };
    const statusEl = $("status");
    let latestOffers = [];
    let latestAuditEvents = [];
    let latestPoolMembers = [];
    let latestHostEnrollments = [];
    let latestHardwareUnits = [];
    let latestTemplateCatalog = [];
    let latestTemplateAssignments = [];
    let latestCertificationRuns = [];
    let latestSettlementWindows = [];
    let latestPayoutBatches = [];

    function auditQuery() {
      const params = new URLSearchParams();
      if (val("auditKind")) params.set("kind", val("auditKind"));
      if (val("auditResourceType")) params.set("resource_type", val("auditResourceType"));
      if (val("auditResourceID")) params.set("resource_id", val("auditResourceID"));
      if (val("auditLimit")) params.set("limit", val("auditLimit"));
      const qs = params.toString();
      return qs ? "/admin/v1/audit-events?" + qs : "/admin/v1/audit-events";
    }

    function latestStatusTransition(kind, resourceType, resourceID) {
      const items = latestAuditEvents || [];
      for (let i = items.length - 1; i >= 0; i--) {
        const item = items[i];
        if (item.kind === kind && item.resource_type === resourceType && item.resource_id === resourceID) {
          return item;
        }
      }
      return null;
    }

    function transitionSummary(item) {
      if (!item || !item.details) return "";
      const from = item.details.from_status || "";
      const to = item.details.to_status || "";
      if (!from && !to) return "";
      return from + " -> " + to;
    }

    // The operator session cookie authenticates /admin/v1 calls; same-origin
    // fetches send it automatically, so no Authorization header is needed.
    function tokenHeaders(includeJSON = true) {
      const headers = {};
      if (includeJSON) headers["Content-Type"] = "application/json";
      return headers;
    }

    function setStatus(msg, cls = "") {
      if (!statusEl) return;
      statusEl.hidden = false;
      statusEl.className = "message" + (cls === "bad" || cls === "error" ? " message-error" : "");
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

    function offerPayloadFromForm() {
      return {
        id: $("offerId").value.trim(),
        capability_id: $("offerCapability").value.trim(),
        offering_id: $("offerOffering").value.trim(),
        protocol: $("offerProtocol").value.trim(),
        work_unit: {
          name: $("offerWorkUnitName").value.trim(),
          extractor: {
            type: $("offerExtractorType").value.trim(),
            expression: $("offerExtractorExpression").value.trim()
          }
        },
        price: {
          amount_wei: $("offerAmountWei").value.trim(),
          per_units: Number($("offerPerUnits").value || "0")
        }
      };
    }

    function syncPayloadTextarea(id, payload) {
      $(id).value = JSON.stringify(payload, null, 2);
      return $(id).value;
    }

    function resetOfferForm() {
      $("offerId").value = "rerank-zerank2";
      $("offerCapability").value = "rerank";
      $("offerOffering").value = "zerank-2-default";
      $("offerProtocol").value = "paid-job/v1";
      $("offerWorkUnitName").value = "requests";
      $("offerExtractorType").value = "request-formula";
      $("offerExtractorExpression").value = "1";
      $("offerAmountWei").value = "372000000000";
      $("offerPerUnits").value = "1";
      $("offerEditorState").textContent = "Creating a new offer.";
      syncPayloadTextarea("offerPayload", offerPayloadFromForm());
    }

    function loadOfferIntoForm(item) {
      $("offerId").value = item.id || "";
      $("offerCapability").value = item.capability_id || "";
      $("offerOffering").value = item.offering_id || "";
      $("offerProtocol").value = item.protocol || "";
      $("offerWorkUnitName").value = (item.work_unit && item.work_unit.name) || "";
      $("offerExtractorType").value = (item.work_unit && item.work_unit.extractor && item.work_unit.extractor.type) || "";
      $("offerExtractorExpression").value = (item.work_unit && item.work_unit.extractor && item.work_unit.extractor.expression) || "";
      $("offerAmountWei").value = (item.price && item.price.amount_wei) || "";
      $("offerPerUnits").value = String((item.price && item.price.per_units) || 1);
      $("offerEditorState").textContent = "Editing existing offer " + item.id + ".";
      syncPayloadTextarea("offerPayload", offerPayloadFromForm());
    }

    function renderOverview() {
      const set = (id, v) => { const e = $(id); if (e) e.textContent = v; };
      set("ovOffers", (latestOffers || []).length);
      set("ovMembers", (latestPoolMembers || []).length);
      set("ovBackends", (latestHardwareUnits || []).length);
      set("ovAssignments", (latestTemplateAssignments || []).length);
    }

    function renderConnectedPool() {
      const set = (id, v) => { const e = $(id); if (e) e.textContent = v; };
      set("poolMemberCount", latestPoolMembers.length);
      set("poolEnrollmentCount", latestHostEnrollments.length);
      set("poolHardwareCount", latestHardwareUnits.length);
      set("poolAssignmentCount", latestTemplateAssignments.length);
      set("poolWindowCount", latestSettlementWindows.filter(item => item.status === "open" || item.status === "closing" || item.status === "pending_approval").length);
      renderSimpleCards("poolMembers", latestPoolMembers, item =>
        "<strong>" + (item.eth_address || item.id) + "</strong>" +
        '<div class="row"><span class="pill">' + (item.status || "unknown") + '</span><span class="pill">' + (item.payout_mode || "eth") + '</span></div>' +
        '<div class="small">' + (item.contact || item.display_name || "") + '</div>'
      );
      renderSimpleCards("poolEnrollments", latestHostEnrollments, item =>
        "<strong>" + item.id + "</strong>" +
        '<div class="row"><span class="pill">' + (item.status || "unknown") + '</span></div>' +
        '<div class="small">' + (item.host_label || "unlabeled host") + '</div>' +
        '<div class="mono">' + (item.member_eth_address || "") + '</div>' +
        ((item.status === "active" || item.status === "pending") ? '<div class="row"><button class="secondary" data-enrollment-revoke="' + item.id + '">Revoke</button></div>' : "")
      );
      const enrollmentHost = $("poolEnrollments");
      if (enrollmentHost) {
        enrollmentHost.querySelectorAll("[data-enrollment-revoke]").forEach(btn => btn.onclick = async () => {
          try {
            setStatus("Revoking host enrollment...");
            await api("/admin/v1/host-enrollments/" + btn.dataset.enrollmentRevoke + "/revoke", { method: "POST", body: "{}" });
            await refreshAll();
          } catch (err) {
            setStatus(err.message, "bad");
          }
        });
      }
      renderSimpleCards("poolHardware", latestHardwareUnits, item =>
        "<strong>" + (item.gpu_model || item.id) + "</strong>" +
        '<div class="row"><span class="pill">' + (item.state || "unknown") + '</span><span class="pill">' + (item.gpu_uuid || "no uuid") + '</span></div>' +
        '<div class="small">host ' + (item.enrollment_id || "") + '</div>'
      );
      renderSimpleCards("poolTemplates", latestTemplateCatalog, item =>
        "<strong>" + item.id + "</strong>" +
        '<div class="row"><span class="pill">' + (item.status || "unknown") + '</span><span class="pill">' + (item.protocol || "") + '</span></div>' +
        '<div class="small">' + (item.capability_id || "") + " / " + (item.offering_id || "") + '</div>'
      );
      renderSimpleCards("poolAssignments", latestTemplateAssignments, item =>
        "<strong>" + item.id + "</strong>" +
        '<div class="row"><span class="pill">' + (item.state || "unknown") + '</span><span class="pill">' + (item.role || "primary") + '</span></div>' +
        '<div class="small">' + (item.template_id || "") + " on " + (item.hardware_unit_id || "") + '</div>' +
        ((item.state === "pending" || item.state === "throttled") ? '<div class="row"><button class="secondary" data-cert-start="' + item.id + '">Start certification</button></div>' : "")
      );
      const assignmentHost = $("poolAssignments");
      if (assignmentHost) {
        assignmentHost.querySelectorAll("[data-cert-start]").forEach(btn => btn.onclick = async () => {
          try {
            setStatus("Starting certification...");
            await api("/admin/v1/template-assignments/" + btn.dataset.certStart + "/certification/start", { method: "POST", body: "{}" });
            await refreshAll();
          } catch (err) {
            setStatus(err.message, "bad");
          }
        });
      }
      renderSimpleCards("poolCertificationRuns", latestCertificationRuns, item =>
        "<strong>" + item.id + "</strong>" +
        '<div class="row"><span class="pill">' + (item.status || "unknown") + '</span><span class="pill">' + (item.execution_path || "") + '</span></div>' +
        '<div class="small">' + (item.assignment_id || "") + '</div>' +
        (item.status === "running" ? '<div class="row"><button class="secondary" data-cert-pass="' + item.id + '">Pass</button><button class="secondary" data-cert-fail="' + item.id + '">Fail</button></div>' : "")
      );
      const certHost = $("poolCertificationRuns");
      if (certHost) {
        certHost.querySelectorAll("[data-cert-pass]").forEach(btn => btn.onclick = async () => {
          try {
            setStatus("Completing certification...");
            await api("/admin/v1/certification-runs/" + btn.dataset.certPass + "/complete", { method: "POST", body: JSON.stringify({ passed: true }) });
            await refreshAll();
          } catch (err) {
            setStatus(err.message, "bad");
          }
        });
        certHost.querySelectorAll("[data-cert-fail]").forEach(btn => btn.onclick = async () => {
          try {
            const failure_reason = window.prompt("Failure reason", "smoke failed") || "smoke failed";
            setStatus("Failing certification...");
            await api("/admin/v1/certification-runs/" + btn.dataset.certFail + "/complete", { method: "POST", body: JSON.stringify({ passed: false, failure_reason }) });
            await refreshAll();
          } catch (err) {
            setStatus(err.message, "bad");
          }
        });
      }
      renderSimpleCards("poolSettlementWindows", latestSettlementWindows, item =>
        "<strong>" + item.id + "</strong>" +
        '<div class="row"><span class="pill">' + (item.status || "unknown") + '</span><span class="pill">scale ' + (item.settlement_scale_ppm || 0) + ' ppm</span></div>' +
        '<div class="small">attributed ' + (item.attributed_revenue_wei || "0") + " / confirmed " + (item.confirmed_revenue_wei || "0") + '</div>'
      );
      renderSimpleCards("poolPayoutBatches", latestPayoutBatches, item =>
        "<strong>" + item.id + "</strong>" +
        '<div class="row"><span class="pill">' + (item.status || "unknown") + '</span><span class="pill">' + ((item.line_items || []).length) + ' rows</span></div>' +
        '<div class="small">total ' + (item.total_amount_wei || "0") + '</div>' +
        (item.status === "pending_approval" ? '<div class="row"><button class="secondary" data-payout-approve="' + item.id + '">Approve</button></div>' : "")
      );
      const payoutHost = $("poolPayoutBatches");
      if (payoutHost) {
        payoutHost.querySelectorAll("[data-payout-approve]").forEach(btn => btn.onclick = async () => {
          try {
            setStatus("Approving payout batch...");
            await api("/admin/v1/payout-batches/" + btn.dataset.payoutApprove + "/approve", { method: "POST", body: "{}" });
            await refreshAll();
          } catch (err) {
            setStatus(err.message, "bad");
          }
        });
      }
    }

    function renderSimpleCards(hostID, items, render) {
      const host = $(hostID);
      if (!host) return;
      host.innerHTML = "";
      if (!items || !items.length) {
        const empty = document.createElement("div");
        empty.className = "card";
        empty.innerHTML = '<span class="muted">No records</span>';
        host.appendChild(empty);
        return;
      }
      items.forEach(item => host.appendChild(card(render(item))));
    }

    async function refreshAll() {
      setStatus("Refreshing control-plane state...");
      try {
        const [auditEvents, offers, poolMembers, hostEnrollments, hardwareUnits, templateCatalog, templateAssignments, certificationRuns, settlementWindows, payoutBatches] = await Promise.all([
          api(auditQuery()),
          api("/admin/v1/offers"),
          api("/admin/v1/pool-members"),
          api("/admin/v1/host-enrollments"),
          api("/admin/v1/hardware-units"),
          api("/admin/v1/template-catalog"),
          api("/admin/v1/template-assignments"),
          api("/admin/v1/certification-runs"),
          api("/admin/v1/settlement-windows"),
          api("/admin/v1/payout-batches")
        ]);

        latestOffers = offers.offers || [];
        latestPoolMembers = poolMembers.pool_members || [];
        latestHostEnrollments = hostEnrollments.host_enrollments || [];
        latestHardwareUnits = hardwareUnits.hardware_units || [];
        latestTemplateCatalog = templateCatalog.templates || [];
        latestTemplateAssignments = templateAssignments.assignments || [];
        latestCertificationRuns = certificationRuns.certification_runs || [];
        latestSettlementWindows = settlementWindows.settlement_windows || [];
        latestPayoutBatches = payoutBatches.payout_batches || [];
        latestAuditEvents = auditEvents.events || [];
        renderAuditEvents(auditEvents.events || []);
        renderOffers(offers.offers || []);
        renderConnectedPool();
        renderOverview();
        setStatus("Control-plane state refreshed.", "ok");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    }

    function renderOffers(items) {
      const host = $("offers");
      if (!host) return;
      host.innerHTML = "";
      items.forEach(item => {
        const el = card(
          "<strong>" + item.id + "</strong>" +
          '<div class="row"><span class="pill">' + item.capability_id + '</span><span class="pill">' + item.offering_id + '</span><span class="pill">' + item.status + '</span></div>' +
          '<div class="small">protocol: ' + item.protocol + '</div>' +
          '<div class="mono">price: ' + item.price.amount_wei + " / " + item.price.per_units + '</div>' +
          '<div class="row">' +
            '<button data-offer-edit="' + item.id + '" class="secondary">Load Into Editor</button>' +
            '<button data-offer-active="' + item.id + '" class="secondary">Set Active</button>' +
            '<button data-offer-disabled="' + item.id + '" class="secondary">Disable</button>' +
          '</div>'
        );
        host.appendChild(el);
      });
      host.querySelectorAll("[data-offer-edit]").forEach(btn => btn.onclick = () => {
        const item = items.find(offer => offer.id === btn.dataset.offerEdit);
        if (item) loadOfferIntoForm(item);
      });
      host.querySelectorAll("[data-offer-active]").forEach(btn => btn.onclick = () => patchOfferStatus(btn.dataset.offerActive, "active"));
      host.querySelectorAll("[data-offer-disabled]").forEach(btn => btn.onclick = () => patchOfferStatus(btn.dataset.offerDisabled, "disabled"));
    }

    function renderAuditEvents(items) {
      const host = $("auditEvents");
      if (!host) return;
      host.innerHTML = "";
      items.slice().reverse().slice(0, 20).forEach(item => {
        const details = item.details ? JSON.stringify(item.details) : "";
        const el = card(
          "<strong>" + item.kind + '</strong>' +
          '<div class="small">' + (item.resource_type || "") + ': ' + (item.resource_id || "") + '</div>' +
          '<div class="small">' + (item.occurred_at || "") + '</div>' +
          (details ? '<div class="mono">' + details + '</div>' : '') +
          '<div class="row">' +
            '<button data-audit-drill="' + (item.resource_type || "") + '|' + (item.resource_id || "") + '" class="secondary">Drill Down</button>' +
          '</div>'
        );
        host.appendChild(el);
      });
      host.querySelectorAll("[data-audit-drill]").forEach(btn => btn.onclick = () => {
        const parts = btn.dataset.auditDrill.split("|");
        if ($("auditResourceType")) $("auditResourceType").value = parts[0] || "";
        if ($("auditResourceID")) $("auditResourceID").value = parts[1] || "";
        void refreshAll();
      });
    }

    async function submitJSON(path, payload, method = "POST") {
      await api(path, { method, body: payload });
      await refreshAll();
    }

    async function patchOfferStatus(id, status) {
      try {
        setStatus("Updating offer status...");
        await submitJSON("/admin/v1/offers/" + id, JSON.stringify({ status }), "PATCH");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    }

    on("refresh", "click", () => { void refreshAll(); });
    on("applyAuditFilters", "click", () => { void refreshAll(); });
    on("clearAuditFilters", "click", () => {
      if ($("auditKind")) $("auditKind").value = "";
      if ($("auditResourceType")) $("auditResourceType").value = "";
      if ($("auditResourceID")) $("auditResourceID").value = "";
      if ($("auditLimit")) $("auditLimit").value = "20";
      void refreshAll();
    });
    on("syncOfferPayload", "click", () => syncPayloadTextarea("offerPayload", offerPayloadFromForm()));
    on("createOffer", "click", async () => {
      try {
        setStatus("Creating offer...");
        await submitJSON("/admin/v1/offers", syncPayloadTextarea("offerPayload", offerPayloadFromForm()));
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
    on("updateOffer", "click", async () => {
      try {
        const id = val("offerId");
        if (!id) throw new Error("Offer ID is required");
        setStatus("Updating offer...");
        await submitJSON("/admin/v1/offers/" + encodeURIComponent(id), syncPayloadTextarea("offerPayload", offerPayloadFromForm()), "PATCH");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
    on("resetOfferForm", "click", () => resetOfferForm());
    on("submitOfferRaw", "click", async () => {
      try {
        setStatus("Submitting raw offer JSON...");
        await submitJSON("/admin/v1/offers", $("offerPayload").value);
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
    on("applyRuntime", "click", async () => {
      try {
        setStatus("Applying desired runtime...");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
    on("markApplied", "click", async () => {
      try {
        setStatus("Marking desired revision applied...");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
    on("markStarted", "click", async () => {
      try {
        setStatus("Marking apply started...");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
    on("markFailed", "click", async () => {
      try {
        const error = window.prompt("Apply failure reason", "reload failed") || "";
        setStatus("Marking apply failed...");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });

    if ($("offerId")) resetOfferForm();
    refreshAll();
