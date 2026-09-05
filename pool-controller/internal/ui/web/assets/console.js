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
    let latestPlacementPlan = null;
    let latestExceptions = null;
    let latestPayoutPolicy = null;
    // The ladder run and the per-batch policy review are POSTs: they exist
    // only because an operator asked for them, so they are held here for
    // the page rather than refetched.
    let latestLadderRun = null;
    let latestPolicyReviews = {};
    // Routes fetched with soft() record their failure here instead of
    // rejecting the refresh. A blank console is a worse answer than a
    // console with one panel missing and a message saying which.
    let softErrors = [];

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

    // Same fetch, but a failure is reported rather than thrown. Used for
    // the derived surfaces (placement plan, exception queue, payout
    // policy) so that e.g. an unparseable policy file cannot take the
    // whole console down with it.
    async function soft(path) {
      try {
        return await api(path);
      } catch (err) {
        softErrors.push(path + ": " + err.message);
        return null;
      }
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
      const view = latestExceptions;
      set("ovExceptions", view
        ? ((view.suspended_members || []).length + (view.suspended_hardware || []).length +
           (view.held_windows || []).length + (view.duplicate_gpus || []).length)
        : "—");
    }

    function renderConnectedPool() {
      const set = (id, v) => { const e = $(id); if (e) e.textContent = v; };
      set("poolMemberCount", latestPoolMembers.length);
      set("poolEnrollmentCount", latestHostEnrollments.length);
      set("poolHardwareCount", latestHardwareUnits.length);
      set("poolAssignmentCount", latestTemplateAssignments.length);
      set("poolWindowCount", latestSettlementWindows.filter(item => item.status === "open" || item.status === "closing" || item.status === "pending_approval").length);
      renderSimpleCards("poolMembers", latestPoolMembers, item =>
        "<strong>" + esc(item.eth_address || item.id) + "</strong>" +
        '<div class="row"><span class="pill">' + esc(item.status || "unknown") + '</span><span class="pill">' + esc(item.payout_mode || "eth") + '</span></div>' +
        '<div class="small">' + esc(item.contact || item.display_name || "") + '</div>'
      );
      renderSimpleCards("poolEnrollments", latestHostEnrollments, item =>
        "<strong>" + esc(item.id) + "</strong>" +
        '<div class="row"><span class="pill">' + esc(item.status || "unknown") + '</span></div>' +
        '<div class="small">' + esc(item.host_label || "unlabeled host") + '</div>' +
        '<div class="mono">' + esc(item.member_eth_address || "") + '</div>' +
        ((item.status === "active" || item.status === "pending") ? '<div class="row"><button class="secondary" data-enrollment-revoke="' + esc(item.id) + '">Revoke</button></div>' : "")
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
        "<strong>" + esc(item.gpu_model || item.id) + "</strong>" +
        '<div class="row"><span class="pill">' + esc(item.state || "unknown") + '</span><span class="pill">' + esc(item.gpu_uuid || "no uuid") + '</span></div>' +
        '<div class="small">host ' + esc(item.enrollment_id || "") + '</div>'
      );
      renderSimpleCards("poolTemplates", latestTemplateCatalog, item =>
        "<strong>" + esc(item.display_name || item.id) + "</strong>" +
        '<div class="row"><span class="pill">' + templateState(item) + '</span><span class="pill">' + esc(item.protocol) + '</span></div>' +
        '<div class="small">' + esc(item.capability) + " / " + esc(item.offering_id) + '</div>' +
        '<div class="small">' + priceLabel(item.effective_price) + '</div>'
      );
      renderSimpleCards("poolAssignments", latestTemplateAssignments, item =>
        "<strong>" + esc(item.id) + "</strong>" +
        '<div class="row"><span class="pill">' + esc(item.state || "unknown") + '</span><span class="pill">' + esc(item.role || "primary") + '</span></div>' +
        '<div class="small">' + esc(item.template_id || "") + " on " + esc(item.hardware_unit_id || "") + '</div>' +
        ((item.state === "pending" || item.state === "throttled") ? '<div class="row"><button class="secondary" data-cert-start="' + esc(item.id) + '">Start certification</button></div>' : "")
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
        "<strong>" + esc(item.id) + "</strong>" +
        '<div class="row"><span class="pill">' + esc(item.status || "unknown") + '</span><span class="pill">' + esc(item.execution_path || "") + '</span></div>' +
        '<div class="small">' + esc(item.assignment_id || "") + '</div>' +
        (item.status === "running" ? '<div class="row"><button class="secondary" data-cert-pass="' + esc(item.id) + '">Pass</button><button class="secondary" data-cert-fail="' + esc(item.id) + '">Fail</button></div>' : "")
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
        "<strong>" + esc(item.id) + "</strong>" +
        '<div class="row"><span class="pill">' + esc(item.status || "unknown") + '</span><span class="pill">scale ' + esc(item.settlement_scale_ppm || 0) + ' ppm</span></div>' +
        '<div class="small">attributed ' + esc(item.attributed_revenue_wei || "0") + " / confirmed " + esc(item.confirmed_revenue_wei || "0") + '</div>'
      );
      renderSimpleCards("poolPayoutBatches", latestPayoutBatches, item =>
        "<strong>" + esc(item.id) + "</strong>" +
        '<div class="row"><span class="pill">' + esc(item.status || "unknown") + '</span><span class="pill">' + ((item.line_items || []).length) + ' rows</span></div>' +
        '<div class="small">total ' + esc(item.total_amount_wei || "0") + '</div>' +
        (item.status === "pending_approval" ? '<div class="row"><button class="secondary" data-payout-approve="' + esc(item.id) + '">Approve</button></div>' : "")
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

    function renderSimpleCards(hostID, items, render, emptyText) {
      const host = $(hostID);
      if (!host) return;
      host.innerHTML = "";
      if (!items || !items.length) {
        const empty = document.createElement("div");
        empty.className = "card";
        empty.innerHTML = '<span class="muted">' + esc(emptyText || "No records") + '</span>';
        host.appendChild(empty);
        return;
      }
      items.forEach(item => host.appendChild(card(render(item))));
    }


    // ---------------------------------------------------------------
    // Placement
    //
    // Placement is deterministic policy over declared facts, so the plan
    // is worth reading before it is applied. An operator on this page is
    // usually asking "why is that card idle", and the answer is a
    // rejection reason code — which is why rejections are rendered
    // beside the placements rather than hidden behind a toggle.
    // ---------------------------------------------------------------
    function renderPlacement() {
      if (!$("placementDecisions")) return;
      const set = (id, v) => { const e = $(id); if (e) e.textContent = v; };
      const plan = latestPlacementPlan || {};
      const decisions = plan.decisions || [];
      const changes = plan.changes || [];
      const notEnabled = plan.not_enabled || [];
      let placed = 0;
      let rejected = 0;
      decisions.forEach(item => {
        placed += (item.placements || []).length;
        rejected += (item.rejections || []).length;
      });
      set("plDecisionCount", latestPlacementPlan ? decisions.length : "—");
      set("plPlacedCount", latestPlacementPlan ? placed : "—");
      set("plRejectedCount", latestPlacementPlan ? rejected : "—");
      set("plChangeCount", latestPlacementPlan ? changes.length : "—");
      set("plGeneratedAt", plan.generated_at || "unavailable");

      const notEnabledHost = $("placementNotEnabled");
      if (notEnabledHost) {
        notEnabledHost.innerHTML = notEnabled.length
          ? notEnabled.map(id => '<span class="badge">' + esc(id) + "</span>").join("")
          : '<span class="small muted">Every catalog template this pool knows about is enabled.</span>';
      }

      const applyBtn = $("placementApply");
      if (applyBtn) {
        applyBtn.disabled = !changes.length;
        applyBtn.textContent = !latestPlacementPlan
          ? "Plan unavailable"
          : (changes.length
            ? "Apply plan (" + changes.length + " change" + (changes.length === 1 ? "" : "s") + ")"
            : "Nothing to apply");
      }

      renderSimpleCards("placementChanges", changes, item =>
        "<strong>" + esc(item.kind || "change") + "</strong>" +
        '<div class="row">' +
          '<span class="pill ' + changePillClass(item.kind) + '">' + esc(item.kind || "change") + "</span>" +
          (item.role ? '<span class="pill">role ' + esc(item.role) + "</span>" : "") +
        "</div>" +
        '<div class="mono">' + esc(item.template_id || "") + " on " + esc(item.hardware_unit_id || "") + "</div>" +
        '<div class="small">' + esc(item.reason || "no reason recorded") + "</div>",
        latestPlacementPlan ? "The fleet already matches the plan — nothing to apply." : "Placement plan unavailable.");

      renderSimpleCards("placementDecisions", decisions, item => {
        const placements = (item.placements || []).map(entry =>
          '<div class="check">' +
            "<strong>" + esc(entry.template_id || "") + "</strong>" +
            '<div class="row"><span class="pill">role ' + esc(entry.role || "primary") + "</span></div>" +
            '<div class="small">' + esc(entry.reason || "no reason recorded") + "</div>" +
          "</div>").join("");
        const rejections = (item.rejections || []).map(entry =>
          '<div class="check">' +
            "<strong>" + esc(entry.template_id || "") + "</strong>" +
            '<div class="row"><span class="pill pill-warn">' + esc(entry.reason || "rejected") + "</span></div>" +
            (entry.detail ? '<div class="small">' + esc(entry.detail) + "</div>" : "") +
          "</div>").join("");
        return "<strong>" + esc(item.hardware_unit_id || "") + "</strong>" +
          '<div class="row">' +
            '<span class="pill">' + esc(item.gpu_class || "unclassified") + "</span>" +
            '<span class="pill">' + (item.placements || []).length + " placed</span>" +
            '<span class="pill ' + ((item.rejections || []).length ? "pill-warn" : "") + '">' + (item.rejections || []).length + " rejected</span>" +
          "</div>" +
          '<div class="mono">member ' + esc(item.member_eth_address || "unknown") + "</div>" +
          '<div class="small">host ' + esc(item.host_enrollment_id || "unenrolled") + "</div>" +
          '<div class="small"><strong>Placed</strong></div>' +
          '<div class="check-list">' + (placements || '<div class="check"><span class="muted">Nothing placed on this GPU.</span></div>') + "</div>" +
          '<div class="small"><strong>Rejected</strong></div>' +
          '<div class="check-list">' + (rejections || '<div class="check"><span class="muted">No template was refused for this GPU.</span></div>') + "</div>";
      }, latestPlacementPlan ? "No hardware to judge." : "Placement plan unavailable.");
    }

    function changePillClass(kind) {
      if (kind === "drain") return "pill-danger";
      if (kind === "role_change") return "pill-warn";
      return "pill-ok";
    }

    // ---------------------------------------------------------------
    // Ladder
    // ---------------------------------------------------------------
    function renderLadder() {
      if (!$("ladderTransitions")) return;
      const set = (id, v) => { const e = $(id); if (e) e.textContent = v; };
      const run = latestLadderRun;
      set("ladderSeeded", run ? (run.seeded || 0) : "—");
      set("ladderEvaluated", run ? (run.evaluated || 0) : "—");
      set("ladderTransitionCount", run ? (run.transitions || []).length : "—");
      const meta = $("ladderMeta");
      if (meta && run) {
        const moved = (run.transitions || []).length;
        meta.textContent = moved
          ? "Last run evaluated " + (run.evaluated || 0) + " placement(s) and moved " + moved + "."
          : "Last run evaluated " + (run.evaluated || 0) + " placement(s) and moved none — the ladder is evaluated far more often than it moves.";
      }
      if (!run) return;
      renderSimpleCards("ladderTransitions", run.transitions || [], item =>
        "<strong>" + esc(item.assignment_id || "") + "</strong>" +
        '<div class="row">' +
          '<span class="pill">' + esc(item.from || "unset") + "</span>" +
          '<span class="pill pill-accent">&rarr; ' + esc(item.to || "") + "</span>" +
          '<span class="pill">' + esc(item.reason_code || "no reason code") + "</span>" +
        "</div>" +
        '<div class="small">' + esc(item.evidence || "no evidence recorded") + "</div>" +
        '<div class="small">share ' + esc(item.share_ppm || 0) + " ppm" +
        (item.max_in_flight ? ", max in flight " + esc(item.max_in_flight) : "") + "</div>" +
        '<div class="small muted">' + esc(item.at || "") + "</div>",
        "This run moved nothing.");
    }

    // ---------------------------------------------------------------
    // Exceptions — the queue of judgements policy refuses to make.
    // ---------------------------------------------------------------
    function renderExceptions() {
      if (!$("exSuspendedMembers")) return;
      const set = (id, v) => { const e = $(id); if (e) e.textContent = v; };
      const view = latestExceptions || {};
      const suspendedMembers = view.suspended_members || [];
      const suspendedHardware = view.suspended_hardware || [];
      const heldWindows = view.held_windows || [];
      const duplicateGPUs = view.duplicate_gpus || [];
      set("exMemberCount", latestExceptions ? suspendedMembers.length : "—");
      set("exHardwareCount", latestExceptions ? suspendedHardware.length : "—");
      set("exWindowCount", latestExceptions ? heldWindows.length : "—");
      set("exDuplicateCount", latestExceptions ? duplicateGPUs.length : "—");
      set("exGeneratedAt", view.generated_at || "unavailable");

      renderSimpleCards("exSuspendedMembers", suspendedMembers, item =>
        "<strong>" + esc(item.eth_address || item.id) + "</strong>" +
        '<div class="row"><span class="pill pill-danger">' + esc(item.status || "suspended") + "</span>" +
          '<span class="pill">' + esc(item.payout_mode || "eth") + "</span></div>" +
        '<div class="small">' + esc(item.display_name || item.contact || "no display name") + "</div>" +
        '<div class="small muted">updated ' + esc(item.updated_at || "") + "</div>" +
        '<div class="row"><label class="small">reason <input data-status-reason placeholder="optional: why this member is coming back"></label></div>' +
        '<div class="row"><button class="secondary" data-member-reinstate="' + esc(item.eth_address || "") + '">Reinstate</button></div>',
        latestExceptions ? "No member is suspended." : "Exception queue unavailable.");
      wireMemberStatusButtons("exSuspendedMembers", "memberReinstate", "active");

      const activeMembers = (latestPoolMembers || []).filter(item => item.status !== "suspended");
      renderSimpleCards("exActiveMembers", activeMembers, item =>
        "<strong>" + esc(item.eth_address || item.id) + "</strong>" +
        '<div class="row"><span class="pill">' + esc(item.status || "active") + "</span>" +
          '<span class="pill">' + esc(item.payout_mode || "eth") + "</span></div>" +
        '<div class="small">' + esc(item.display_name || item.contact || "no display name") + "</div>" +
        '<div class="row"><label class="small">reason (required) <input data-status-reason placeholder="why this member is being suspended"></label></div>' +
        '<div class="row"><button class="secondary" data-member-suspend="' + esc(item.eth_address || "") + '" disabled>Suspend</button>' +
          '<span class="small muted" data-status-hint>A suspension with no reason is a decision nobody can review later.</span></div>',
        "No active members.");
      wireMemberStatusButtons("exActiveMembers", "memberSuspend", "suspended");

      renderSimpleCards("exSuspendedHardware", suspendedHardware, item =>
        "<strong>" + esc(item.gpu_model || item.id) + "</strong>" +
        '<div class="row"><span class="pill pill-danger">' + esc(item.state || "suspended") + "</span>" +
          '<span class="pill">' + esc(item.gpu_uuid || "no uuid") + "</span></div>" +
        '<div class="mono">member ' + esc(item.member_eth_address || "") + "</div>" +
        '<div class="small">host ' + esc(item.enrollment_id || "") + "</div>" +
        '<div class="small muted">last seen ' + esc(item.last_seen_at || "never") + "</div>",
        latestExceptions ? "No hardware is suspended." : "Exception queue unavailable.");

      renderSimpleCards("exHeldWindows", heldWindows, item =>
        "<strong>" + esc(item.window_id || "") + "</strong>" +
        '<div class="row"><span class="pill pill-warn">' + esc(item.status || "held") + "</span>" +
          '<span class="pill">scale ' + esc(item.settlement_scale_ppm || 0) + " ppm</span></div>" +
        '<div class="small">' + (item.anomaly ? "anomaly: " + esc(item.anomaly) : "no anomaly recorded — held for approval") + "</div>",
        latestExceptions ? "No window is waiting on a person." : "Exception queue unavailable.");

      renderSimpleCards("exDuplicateGPUs", duplicateGPUs, item =>
        "<strong>" + esc(item.gpu_uuid || "") + "</strong>" +
        '<div class="row"><span class="pill pill-danger">' + (item.member_eth_addresses || []).length + " claimants</span></div>" +
        (item.member_eth_addresses || []).map(addr => '<div class="mono">' + esc(addr) + "</div>").join(""),
        latestExceptions ? "No GPU is claimed by two members." : "Exception queue unavailable.");
    }

    // The reason field is enforced here as well as at the server: the
    // server refuses a reasonless suspension with a 400, and an operator
    // should learn that from a disabled button, not from a red banner
    // after the fact.
    function wireMemberStatusButtons(hostID, dataKey, status) {
      const host = $(hostID);
      if (!host) return;
      const attr = dataKey === "memberSuspend" ? "[data-member-suspend]" : "[data-member-reinstate]";
      host.querySelectorAll(attr).forEach(btn => {
        const scope = btn.closest(".card");
        const input = scope ? scope.querySelector("[data-status-reason]") : null;
        const hint = scope ? scope.querySelector("[data-status-hint]") : null;
        if (status === "suspended" && input) {
          const sync = () => {
            const ready = input.value.trim().length > 0;
            btn.disabled = !ready;
            if (hint) hint.textContent = ready ? "Suspending drains this member's placements." : "A reason is required to suspend.";
          };
          input.addEventListener("input", sync);
          sync();
        }
        btn.onclick = async () => {
          const reason = input ? input.value.trim() : "";
          if (status === "suspended" && !reason) {
            setStatus("A reason is required when suspending a member.", "bad");
            return;
          }
          try {
            setStatus(status === "suspended" ? "Suspending member..." : "Reinstating member...");
            const address = btn.dataset.memberSuspend || btn.dataset.memberReinstate;
            const result = await api("/admin/v1/pool-members/" + encodeURIComponent(address), {
              method: "PATCH",
              body: JSON.stringify({ status: status, reason: reason, actor: currentActor() })
            });
            await refreshAll();
            if (status === "suspended") {
              setStatus("Member suspended; " + ((result && result.drained_placements) || 0) + " placement(s) draining.", "ok");
            } else {
              setStatus("Member reinstated.", "ok");
            }
          } catch (err) {
            setStatus(err.message, "bad");
          }
        };
      });
    }

    // ---------------------------------------------------------------
    // Payouts — the policy in force, and what it decides per batch.
    //
    // Whether automation is armed is the first thing this page has to
    // answer, so paused and shadow are stated as a banner before any
    // batch is listed.
    // ---------------------------------------------------------------
    function renderPayouts() {
      const banner = $("payoutPolicyBanner");
      if (!banner) return;
      const view = latestPayoutPolicy;
      const policy = (view && view.policy) || {};
      const auto = policy.auto_approve || {};
      const paused = !!(view && view.paused);
      const shadow = !!policy.shadow;
      const enabled = !!auto.enabled;

      let cls = "message-card-info";
      let title = "Policy unavailable";
      let body = "The payout policy could not be read; automatic approval cannot be assumed either way.";
      if (view) {
        if (paused) {
          cls = "message-card-error";
          title = "PAUSED — automation is disarmed";
          body = "A pause file is in place. No batch will be approved by policy until it is removed; approval is human-only right now.";
        } else if (!enabled) {
          cls = "message-card-info";
          title = "OFF — automatic approval is not enabled";
          body = "The policy exists but does not authorise automatic approval. Every batch waits for a person.";
        } else if (shadow) {
          cls = "message-card-warn";
          title = "SHADOW — records verdicts, approves nothing";
          body = "The policy evaluates each batch and records what it WOULD have decided. Nothing is approved automatically; divergence from human approvals is what earns the move to live.";
        } else {
          cls = "message-card-ok";
          title = "LIVE — policy can approve batches without a person";
          body = "Automatic approval is armed and not paused. A batch inside the bounds below is approved by policy the moment it is reviewed.";
        }
      }
      banner.className = "card message-card " + cls;
      banner.innerHTML = "<h2>" + esc(title) + "</h2><p>" + esc(body) + "</p>" +
        '<p class="small">' +
          "mode " + (shadow ? "shadow" : "live") +
          " &middot; auto-approve " + (enabled ? "enabled" : "disabled") +
          " &middot; " + (paused ? "paused" : "not paused") +
        "</p>";

      const detail = $("payoutPolicyDetail");
      if (detail) {
        detail.innerHTML = "";
        detail.appendChild(card(
          "<strong>Policy file</strong>" +
          '<div class="row">' +
            '<span class="pill ' + (shadow ? "pill-warn" : "pill-ok") + '">' + (shadow ? "shadow" : "live") + "</span>" +
            '<span class="pill ' + (enabled ? "pill-ok" : "") + '">auto-approve ' + (enabled ? "enabled" : "disabled") + "</span>" +
            '<span class="pill ' + (paused ? "pill-danger" : "") + '">' + (paused ? "paused" : "not paused") + "</span>" +
          "</div>" +
          '<div class="mono">' + esc((view && view.path) || "no policy path configured") + "</div>" +
          '<div class="mono">hash ' + esc((view && view.policy_hash) || "none — no policy file in force") + "</div>" +
          '<div class="small">Every decision this policy makes is recorded in the audit trail beside this hash, so an audit can prove which rules were in force.</div>' +
          '<div class="small">bounds: max batch ' + esc(auto.max_batch_wei || "unbounded") +
            " wei &middot; max per member " + esc(auto.max_per_member_wei || "unbounded") + " wei</div>" +
          '<div class="small">requires settlement scale &ge; ' + esc(auto.require_scale_gte || 0) +
            " &middot; max " + esc(auto.max_batches_per_day || 0) + " batches/day</div>"
        ));
      }

      renderSimpleCards("payoutBatches", latestPayoutBatches, item => {
        const review = latestPolicyReviews[item.id];
        const reviewBlock = review
          ? '<div class="check">' +
              "<strong>Policy review — " + (review.approved ? "approved" : "refused") + "</strong>" +
              '<div class="row">' +
                '<span class="pill ' + (review.approved ? "pill-ok" : "pill-warn") + '">' + (review.approved ? "approved" : "refused") + "</span>" +
                '<span class="pill ' + (review.shadow ? "pill-warn" : "") + '">' + (review.shadow ? "shadow — nothing approved" : "live") + "</span>" +
              "</div>" +
              '<div class="small">' + esc(review.reason || "no reason recorded") + "</div>" +
              '<div class="mono">policy ' + esc(review.policy_hash || "none") + "</div>" +
            "</div>"
          : "";
        return "<strong>" + esc(item.id || "") + "</strong>" +
          '<div class="row">' +
            '<span class="pill">' + esc(item.status || "unknown") + "</span>" +
            '<span class="pill">' + ((item.line_items || []).length) + " rows</span>" +
          "</div>" +
          '<div class="small">total ' + esc(item.total_amount_wei || "0") + " wei</div>" +
          '<div class="small muted">window ' + esc(item.settlement_window_id || "") + "</div>" +
          reviewBlock +
          '<div class="row">' +
            '<button class="secondary" data-policy-review="' + esc(item.id || "") + '">Policy review</button>' +
            (item.status === "pending_approval" ? '<button class="secondary" data-payout-approve-live="' + esc(item.id || "") + '">Approve manually</button>' : "") +
          "</div>";
      }, "No payout batches.");

      const host = $("payoutBatches");
      if (!host) return;
      host.querySelectorAll("[data-policy-review]").forEach(btn => btn.onclick = async () => {
        const id = btn.dataset.policyReview;
        try {
          setStatus("Evaluating batch against the payout policy...");
          const decision = await api("/admin/v1/payout-batches/" + encodeURIComponent(id) + "/policy-review", { method: "POST", body: "{}" });
          latestPolicyReviews[id] = decision;
          await refreshAll();
          setStatus("Policy " + (decision.approved ? "approved" : "refused") + " " + id +
            (decision.shadow ? " (shadow — nothing was approved)" : "") + ": " + (decision.reason || ""), decision.approved ? "ok" : "bad");
        } catch (err) {
          setStatus(err.message, "bad");
        }
      });
      host.querySelectorAll("[data-payout-approve-live]").forEach(btn => btn.onclick = async () => {
        try {
          setStatus("Approving payout batch...");
          await api("/admin/v1/payout-batches/" + encodeURIComponent(btn.dataset.payoutApproveLive) + "/approve", { method: "POST", body: "{}" });
          await refreshAll();
        } catch (err) {
          setStatus(err.message, "bad");
        }
      });
    }

    async function refreshAll() {
      setStatus("Refreshing control-plane state...");
      softErrors = [];
      try {
        // Ten hard fetches and three soft ones: thirteen destructured
        // names against thirteen entries, which is the invariant that
        // keeps a mismatched Promise.all from blanking the console.
        const [auditEvents, offers, poolMembers, hostEnrollments, hardwareUnits, templateCatalog, templateAssignments, certificationRuns, settlementWindows, payoutBatches, placementPlan, exceptions, payoutPolicy] = await Promise.all([
          api(auditQuery()),
          api("/admin/v1/offers"),
          api("/admin/v1/pool-members"),
          api("/admin/v1/host-enrollments"),
          api("/admin/v1/hardware-units"),
          api("/admin/v1/template-catalog"),
          api("/admin/v1/template-assignments"),
          api("/admin/v1/certification-runs"),
          api("/admin/v1/settlement-windows"),
          api("/admin/v1/payout-batches"),
          soft("/admin/v1/placement-plan"),
          soft("/admin/v1/exceptions"),
          soft("/admin/v1/payout-policy")
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
        latestPlacementPlan = placementPlan;
        latestExceptions = exceptions;
        latestPayoutPolicy = payoutPolicy;
        renderAuditEvents(latestAuditEvents);
        renderOffers(latestOffers);
        renderTemplateCatalog(latestTemplateCatalog);
        renderConnectedPool();
        renderPlacement();
        renderLadder();
        renderExceptions();
        renderPayouts();
        renderOverview();
        if (softErrors.length) {
          setStatus("Refreshed, but some surfaces failed: " + softErrors.join("; "), "bad");
        } else {
          setStatus("Control-plane state refreshed.", "ok");
        }
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
          "<strong>" + esc(item.kind) + '</strong>' +
          '<div class="small">' + esc(item.resource_type || "") + ': ' + esc(item.resource_id || "") + '</div>' +
          '<div class="small">' + esc(item.occurred_at || "") + '</div>' +
          (details ? '<div class="mono">' + esc(details) + '</div>' : '') +
          '<div class="row">' +
            '<button data-audit-drill="' + esc(item.resource_type || "") + '|' + esc(item.resource_id || "") + '" class="secondary">Drill Down</button>' +
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
    on("placementApply", "click", async () => {
      const changes = (latestPlacementPlan && latestPlacementPlan.changes) || [];
      if (!changes.length) return;
      // Applying creates and role-changes freely but only DRAINS what it
      // withdraws, so the confirm says how many placements stop taking
      // new work rather than pretending it is all additive.
      const drains = changes.filter(item => item.kind === "drain").length;
      const question = "Apply " + changes.length + " placement change(s)" +
        (drains ? ", including " + drains + " drain(s)?" : "?");
      if (!window.confirm(question)) return;
      try {
        setStatus("Applying the placement plan...");
        const result = await api("/admin/v1/placement-plan/apply", { method: "POST", body: "{}" });
        await refreshAll();
        setStatus("Applied " + ((result && result.applied) || []).length + " placement change(s).", "ok");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
    on("ladderRun", "click", async () => {
      try {
        setStatus("Running the selection ladder...");
        latestLadderRun = await api("/admin/v1/ladder/run", { method: "POST", body: "{}" });
        const moved = (latestLadderRun.transitions || []).length;
        await refreshAll();
        setStatus("Ladder evaluated " + (latestLadderRun.evaluated || 0) + " placement(s) and moved " + moved + ".", "ok");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
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
