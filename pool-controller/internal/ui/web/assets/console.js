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

    // Card bodies are assembled as HTML strings, and template text now
    // comes from YAML files rather than from this codebase, so anything
    // interpolated from a response goes through here first.
    function esc(value) {
      return String(value === null || value === undefined ? "" : value)
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;")
        .replace(/'/g, "&#39;");
    }

    function priceLabel(price) {
      if (!price) return "unpriced";
      return esc(price.amount_wei || "0") + " wei / " + esc(price.per_units || 1) + " units";
    }

    // The layout prints the logged-in operator; carrying it into an
    // override makes the stored updated_by name a person instead of
    // leaving the audit trail anonymous.
    function currentActor() {
      const el = document.querySelector(".identity-chip code");
      return el ? el.textContent.trim() : "";
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
        "<strong>" + esc(item.display_name || item.id) + "</strong>" +
        '<div class="row"><span class="pill">' + templateState(item) + '</span><span class="pill">' + esc(item.protocol) + '</span></div>' +
        '<div class="small">' + esc(item.capability) + " / " + esc(item.offering_id) + '</div>' +
        '<div class="small">' + priceLabel(item.effective_price) + '</div>'
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
        renderAuditEvents(latestAuditEvents);
        renderOffers(latestOffers);
        renderTemplateCatalog(latestTemplateCatalog);
        renderConnectedPool();
        renderOverview();
        setStatus("Control-plane state refreshed.", "ok");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    }

    // The offer set has no identity of its own any more: the controller
    // recomputes it from the adopted templates on every read, which is
    // the same computation the broker push runs. So this renders what the
    // fleet was actually sent, and it is deliberately read-only — the
    // template panel is the only place an operator can change it.
    function renderOffers(items) {
      renderSimpleCards("offers", items, item => {
        const match = Object.keys(item.match || {}).map(key => esc(key) + "=" + esc(item.match[key])).join(", ");
        const certification = (item.certification || []).map(step => esc(step.name || step.type || "step")).join(", ");
        const promoted = (item.extra_from_runner || []).map(esc).join(", ");
        const capacity = item.capacity;
        return "<strong>" + esc(item.offering_id) + "</strong>" +
          '<div class="row">' +
            '<span class="pill">' + esc(item.capability) + '</span>' +
            '<span class="pill">' + esc(item.protocol) + '</span>' +
            '<span class="pill">' + (item.disabled ? "pushed, not advertised" : "advertised") + '</span>' +
          '</div>' +
          '<div class="mono">price: ' + priceLabel(item.price) + '</div>' +
          (capacity ? '<div class="small">capacity: ' + esc(capacity.max_in_flight || 0) + " in flight, queue " + esc(capacity.queue_limit || 0) + '</div>' : '<div class="small">capacity: broker default</div>') +
          '<div class="small">match: ' + (match || "any runner serving the capability") + '</div>' +
          '<div class="small">certification: ' + (certification || "none") + '</div>' +
          (promoted ? '<div class="small">runner may promote: ' + promoted + '</div>' : "");
      });
    }

    // The three states an operator has to be able to tell apart. No
    // override at all means the pool never adopted the template and no
    // offer is pushed for it; an override with enabled false still pushes
    // the offer, so the broker keeps it and stops advertising it.
    function templateState(item) {
      if (!item.override_updated_at) return "not adopted";
      return item.enabled ? "enabled" : "disabled";
    }

    function templateByID(id) {
      return (latestTemplateCatalog || []).find(item => item.id === id) || null;
    }

    // A PUT replaces the whole override, so a toggle that only means
    // "enable this" still has to carry everything else the pool set
    // forward — the price, or the offer drops back to the catalog's
    // suggestion, and extra_override, or the pool's advertised metadata
    // is lost. extra_override is the pool's own half unmerged, which is
    // why it can be echoed back: nothing of the catalog's rides along to
    // be frozen where a later catalog edit could not reach it.
    function templateOverrideBody(item, enabled, price) {
      const body = { enabled: enabled, updated_by: currentActor() };
      if (price) {
        body.price = price;
      } else if (item.price_overridden && item.effective_price) {
        body.price = { amount_wei: item.effective_price.amount_wei, per_units: item.effective_price.per_units };
      }
      // Absent and empty are not the same record: send extra only where
      // the pool actually set some.
      if (hasExtraOverride(item)) body.extra = item.extra_override;
      return body;
    }

    function hasExtraOverride(item) {
      return !!item.extra_override && Object.keys(item.extra_override).length > 0;
    }

    async function putTemplateOverride(id, body) {
      try {
        setStatus("Saving template override...");
        await api("/admin/v1/template-overrides/" + encodeURIComponent(id), { method: "PUT", body: JSON.stringify(body) });
        await refreshAll();
      } catch (err) {
        setStatus(err.message, "bad");
      }
    }

    function renderTemplateCatalog(items) {
      renderSimpleCards("templateCatalog", items, item => {
        const price = item.effective_price || {};
        const id = esc(item.id);
        return "<strong>" + esc(item.display_name || item.id) + "</strong>" +
          '<div class="row">' +
            '<span class="pill">' + templateState(item) + '</span>' +
            '<span class="pill">' + esc(item.capability) + '</span>' +
            '<span class="pill">' + esc(item.protocol) + '</span>' +
          '</div>' +
          '<div class="mono">' + id + " to " + esc(item.offering_id) + '</div>' +
          (item.description ? '<div class="small">' + esc(item.description) + '</div>' : "") +
          '<div class="small">price: ' + priceLabel(price) + " (" + (item.price_overridden ? "pool override" : "catalog default") + ")</div>" +
          (hasExtraOverride(item) ? '<div class="row"><span class="pill">extra override: ' + Object.keys(item.extra_override).length + ' key(s)</span></div>' : "") +
          (item.override_updated_at ? '<div class="small muted">override updated ' + esc(item.override_updated_at) + '</div>' : "") +
          '<div class="row">' +
            '<label class="small">amount wei <input data-template-amount value="' + esc(price.amount_wei || "0") + '"></label>' +
            '<label class="small">per units <input data-template-per type="number" min="1" value="' + esc(price.per_units || 1) + '"></label>' +
          '</div>' +
          '<div class="row">' +
            '<button data-template-save="' + id + '">Save price</button>' +
            '<button class="secondary" data-template-enable="' + id + '">' + (item.enabled ? "Disable" : "Enable") + '</button>' +
            (item.override_updated_at ? '<button class="secondary" data-template-revert="' + id + '">Revert to catalog default</button>' : "") +
          '</div>';
      });
      const host = $("templateCatalog");
      if (!host) return;
      host.querySelectorAll("[data-template-save]").forEach(btn => btn.onclick = () => {
        const item = templateByID(btn.dataset.templateSave);
        if (!item) return;
        const scope = btn.closest(".card");
        const amount = scope.querySelector("[data-template-amount]").value.trim();
        const perUnits = Number(scope.querySelector("[data-template-per]").value || "1");
        // Pricing a template the pool never adopted is how it gets
        // adopted; pricing one the operator deliberately turned off must
        // not quietly turn it back on.
        const enabled = item.override_updated_at ? item.enabled : true;
        void putTemplateOverride(item.id, templateOverrideBody(item, enabled, { amount_wei: amount, per_units: perUnits }));
      });
      host.querySelectorAll("[data-template-enable]").forEach(btn => btn.onclick = () => {
        const item = templateByID(btn.dataset.templateEnable);
        if (!item) return;
        void putTemplateOverride(item.id, templateOverrideBody(item, !item.enabled, null));
      });
      host.querySelectorAll("[data-template-revert]").forEach(btn => btn.onclick = async () => {
        try {
          setStatus("Reverting template to catalog default...");
          await api("/admin/v1/template-overrides/" + encodeURIComponent(btn.dataset.templateRevert), { method: "DELETE" });
          await refreshAll();
        } catch (err) {
          setStatus(err.message, "bad");
        }
      });
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

    on("refresh", "click", () => { void refreshAll(); });
    on("applyAuditFilters", "click", () => { void refreshAll(); });
    on("clearAuditFilters", "click", () => {
      if ($("auditKind")) $("auditKind").value = "";
      if ($("auditResourceType")) $("auditResourceType").value = "";
      if ($("auditResourceID")) $("auditResourceID").value = "";
      if ($("auditLimit")) $("auditLimit").value = "20";
      void refreshAll();
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

    refreshAll();
