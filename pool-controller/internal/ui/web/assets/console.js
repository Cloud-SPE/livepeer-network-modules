    const $ = (id) => document.getElementById(id);
    const on = (id, ev, fn) => { const el = $(id); if (el) el.addEventListener(ev, fn); };
    const val = (id) => { const el = $(id); return el ? el.value.trim() : ""; };
    const statusEl = $("status");
    let latestOffers = [];
    let latestMembers = [];
    let latestBackends = [];
    let latestAssignments = [];
    let latestRuntime = null;
    let latestAssignmentPreview = null;
    let latestJoinPreview = null;
    let latestAssignmentCandidates = [];
    let latestAuditEvents = [];
    let latestRuntimeHistory = [];

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
        interaction_mode: $("offerInteraction").value.trim(),
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

    function joinPayloadFromForm() {
      return {
        id: $("joinId").value.trim(),
        member_eth_address: $("joinMemberAddress").value.trim(),
        display_name: $("joinDisplayName").value.trim(),
        payout_mode: $("joinPayoutMode").value,
        requested_backends: [
          {
            id: $("joinBackendId").value.trim(),
            transport: $("joinBackendTransport").value.trim(),
            url: $("joinBackendUrl").value.trim(),
            auth: { method: "none" },
            health_probe: {
              type: "http-status",
              config: { url: $("joinHealthUrl").value.trim() }
            },
            claimed_capabilities: [
              {
                capability_id: $("joinCapability").value.trim(),
                offering_id: $("joinOffering").value.trim(),
                interaction_mode: $("joinInteraction").value.trim()
              }
            ]
          }
        ]
      };
    }

    function assignmentPayloadFromForm() {
      return {
        id: $("assignmentId").value.trim(),
        offer_id: $("assignmentOfferId").value.trim(),
        member_backend_id: $("assignmentBackendId").value.trim()
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
      $("offerInteraction").value = "http-reqresp@v0";
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
      $("offerInteraction").value = item.interaction_mode || "";
      $("offerWorkUnitName").value = (item.work_unit && item.work_unit.name) || "";
      $("offerExtractorType").value = (item.work_unit && item.work_unit.extractor && item.work_unit.extractor.type) || "";
      $("offerExtractorExpression").value = (item.work_unit && item.work_unit.extractor && item.work_unit.extractor.expression) || "";
      $("offerAmountWei").value = (item.price && item.price.amount_wei) || "";
      $("offerPerUnits").value = String((item.price && item.price.per_units) || 1);
      $("offerEditorState").textContent = "Editing existing offer " + item.id + ".";
      syncPayloadTextarea("offerPayload", offerPayloadFromForm());
    }

    function syncAssignmentSelectors() {
      const offerSelect = $("assignmentOfferSelect");
      const backendSelect = $("assignmentBackendSelect");
      if (!offerSelect || !backendSelect) return;
      offerSelect.innerHTML = '<option value="">Select an offer</option>';
      backendSelect.innerHTML = '<option value="">Select a backend</option>';
      latestOffers.forEach(item => {
        const option = document.createElement("option");
        option.value = item.id;
        option.textContent = item.id + " (" + item.capability_id + ")";
        offerSelect.appendChild(option);
      });
      latestBackends.forEach(item => {
        const option = document.createElement("option");
        option.value = item.id;
        option.textContent = item.id + " [" + item.status + ", " + item.verification_status + "]";
        backendSelect.appendChild(option);
      });
    }

    function renderAssignmentPreview(preview) {
      const host = $("assignmentPreviewDetails");
      if (!host) return;
      host.innerHTML = "";
      if (!preview) return;
      const checks = preview.checks || [];
      checks.forEach(check => {
        const div = document.createElement("div");
        div.className = "check";
        div.innerHTML =
          '<strong class="' + (check.passed ? "ok" : "bad") + '">' + check.name + '</strong>' +
          '<div class="small">' + (check.detail || "") + '</div>';
        host.appendChild(div);
      });
      if (preview.matched_claim) {
        const div = document.createElement("div");
        div.className = "check";
        div.innerHTML =
          '<strong class="ok">matched_claim</strong>' +
          '<div class="small">' + preview.matched_claim.capability_id + ' / ' + (preview.matched_claim.offering_id || "") + ' / ' + (preview.matched_claim.interaction_mode || "") + '</div>';
        host.appendChild(div);
      }
      if ((preview.reasons || []).length) {
        const div = document.createElement("div");
        div.className = "check";
        div.innerHTML =
          '<strong class="warn">reasons</strong>' +
          '<div class="small">' + preview.reasons.join("; ") + '</div>';
        host.appendChild(div);
      }
    }

    function renderJoinPreview(preview) {
      const host = $("joinPreviewDetails");
      if (!host) return;
      host.innerHTML = "";
      latestJoinPreview = preview;
      if (!preview) return;
      const summary = document.createElement("div");
      summary.className = "check";
      summary.innerHTML =
        '<strong class="' + (preview.approavable ? "ok" : "bad") + '">join_request</strong>' +
        '<div class="small">id=' + (preview.join_request_id || "") + ', status=' + (preview.status || "") + ', approvable=' + String(!!preview.approavable) + '</div>';
      host.appendChild(summary);
      (preview.backend_previews || []).forEach(item => {
        const div = document.createElement("div");
        div.className = "check";
        const reasons = (item.reasons || []).length ? item.reasons.join("; ") : "";
        div.innerHTML =
          '<strong class="' + (item.approavable ? "ok" : "bad") + '">' + (item.backend_id || "backend") + '</strong>' +
          '<div class="small">' + [item.transport, item.url, item.verification_status, "claims=" + String(item.claim_count || 0), "servable_claims=" + String(item.servable_claim_count || 0)].filter(Boolean).join(" | ") + '</div>' +
          (item.verification_error ? '<div class="small">' + item.verification_error + '</div>' : '') +
          (reasons ? '<div class="small">' + reasons + '</div>' : '');
        host.appendChild(div);
        (item.claim_previews || []).forEach(claim => {
          const claimDiv = document.createElement("div");
          claimDiv.className = "check";
          const claimReasons = (claim.reasons || []).length ? claim.reasons.join("; ") : "";
          const suggestedOfferID = (claim.suggested_offer_ids || [])[0] || "";
          const draftButton = suggestedOfferID
            ? '<div class="row"><button data-join-draft="' + (item.backend_id || "") + '|' + suggestedOfferID + '" class="secondary">Use Suggested Offer In Assignment Draft</button></div>'
            : "";
          claimDiv.innerHTML =
            '<strong class="' + (claim.servable ? "ok" : "warn") + '">claim</strong>' +
            '<div class="small">' + [claim.capability_id || "", claim.offering_id || "", claim.interaction_mode || ""].filter(Boolean).join(" / ") + '</div>' +
            '<div class="small">matching_offers=' + ((claim.matching_offer_ids || []).join(", ") || "none") + '</div>' +
            '<div class="small">active_offers=' + ((claim.active_offer_ids || []).join(", ") || "none") + '</div>' +
            '<div class="small">suggested_offers=' + ((claim.suggested_offer_ids || []).join(", ") || "none") + '</div>' +
            draftButton +
            (claimReasons ? '<div class="small">' + claimReasons + '</div>' : '');
          host.appendChild(claimDiv);
          (claim.suggestions || []).forEach(suggestion => {
            const suggestionDiv = document.createElement("div");
            suggestionDiv.className = "check";
            suggestionDiv.innerHTML =
              '<strong class="ok">suggestion</strong>' +
              '<div class="small">' + (suggestion.offer_id || "") + ' | score=' + String(suggestion.score || 0) + '</div>' +
              (suggestion.reason ? '<div class="small">' + suggestion.reason + '</div>' : '');
            host.appendChild(suggestionDiv);
          });
        });
      });
      host.querySelectorAll("[data-join-draft]").forEach(btn => btn.onclick = () => {
        const parts = btn.dataset.joinDraft.split("|");
        seedAssignmentDraft(parts[0] || "", parts[1] || "");
      });
      if ((preview.reasons || []).length) {
        const div = document.createElement("div");
        div.className = "check";
        div.innerHTML =
          '<strong class="warn">reasons</strong>' +
          '<div class="small">' + preview.reasons.join("; ") + '</div>';
        host.appendChild(div);
      }
    }

    function selectedOffer() {
      return latestOffers.find(item => item.id === val("assignmentOfferId")) || null;
    }

    function selectedBackend() {
      return latestBackends.find(item => item.id === val("assignmentBackendId")) || null;
    }

    async function refreshAssignmentDraftState() {
      if (!$("assignmentDraftState")) return;
      const offer = selectedOffer();
      const backend = selectedBackend();
      const el = $("assignmentDraftState");
      if (!offer && !backend) {
        el.textContent = "Select an offer and backend to draft an assignment.";
        latestAssignmentPreview = null;
        renderAssignmentPreview(null);
        return;
      }
      if (!offer || !backend) {
        el.textContent = "Draft is incomplete. Choose both an offer and a backend.";
        latestAssignmentPreview = null;
        renderAssignmentPreview(null);
        return;
      }
      try {
        const preview = await api("/admin/v1/assignment-preview", {
          method: "POST",
          body: JSON.stringify({
            offer_id: offer.id,
            member_backend_id: backend.id
          })
        });
        latestAssignmentPreview = preview;
        renderAssignmentPreview(preview);
        const summary = [
          "offer=" + offer.id,
          "backend=" + backend.id,
          "backend_status=" + (preview.backend_status || backend.status),
          "verification=" + (preview.verification_status || backend.verification_status)
        ];
        if (preview.compatible) {
          el.textContent = "Draft looks routable: " + summary.join(", ");
        } else {
          el.textContent = "Draft needs attention: " + summary.join(", ") + ". " + (preview.reasons || []).join("; ");
        }
      } catch (err) {
        latestAssignmentPreview = null;
        renderAssignmentPreview(null);
        el.textContent = "Draft preview failed: " + err.message;
      }
    }

    function promoteBackendToAssignmentDraft(backendID) {
      if (!backendID) return;
      $("assignmentBackendId").value = backendID;
      $("assignmentBackendSelect").value = backendID;
      if (!$("assignmentId").value.trim() || $("assignmentId").value.trim() === "assign-sample") {
        $("assignmentId").value = "assign-" + backendID;
      }
      syncPayloadTextarea("assignmentPayload", assignmentPayloadFromForm());
      void refreshAssignmentDraftState();
      setStatus("Backend " + backendID + " promoted into assignment draft.", "ok");
    }

    function promoteOfferToAssignmentDraft(offerID) {
      if (!offerID) return;
      $("assignmentOfferId").value = offerID;
      $("assignmentOfferSelect").value = offerID;
      syncPayloadTextarea("assignmentPayload", assignmentPayloadFromForm());
      void refreshAssignmentDraftState();
      setStatus("Offer " + offerID + " promoted into assignment draft.", "ok");
    }

    function seedAssignmentDraft(backendID, offerID) {
      if (backendID) {
        $("assignmentBackendId").value = backendID;
        $("assignmentBackendSelect").value = backendID;
      }
      if (offerID) {
        $("assignmentOfferId").value = offerID;
        $("assignmentOfferSelect").value = offerID;
      }
      if (backendID && offerID) {
        $("assignmentId").value = "assign-" + backendID + "-" + offerID;
      } else if (backendID && (!$("assignmentId").value.trim() || $("assignmentId").value.trim() === "assign-sample")) {
        $("assignmentId").value = "assign-" + backendID;
      }
      syncPayloadTextarea("assignmentPayload", assignmentPayloadFromForm());
      void refreshAssignmentDraftState();
      setStatus("Assignment draft seeded from join review.", "ok");
    }

    function runtimeSummary(item) {
      if (!item) return "—";
      const state = item.dirty ? "dirty" : "converged";
      const applyStatus = item.last_apply_status || "unapplied";
      return state + " / " + applyStatus;
    }

    function renderOverview() {
      const set = (id, v) => { const e = $(id); if (e) e.textContent = v; };
      set("ovOffers", (latestOffers || []).length);
      set("ovMembers", (latestMembers || []).length);
      set("ovBackends", (latestBackends || []).length);
      set("ovAssignments", (latestAssignments || []).length);
      set("ovRuntime", runtimeSummary(latestRuntime));
    }

    async function refreshAll() {
      setStatus("Refreshing control-plane state...");
      try {
        const [auditEvents, offers, joinRequests, members, backends, assignmentCandidates, assignments, runtime, runtimeHistory, brokerConfig] = await Promise.all([
          api(auditQuery()),
          api("/admin/v1/offers"),
          api("/admin/v1/join-requests"),
          api("/admin/v1/members"),
          api("/admin/v1/member-backends"),
          api("/admin/v1/assignment-candidates"),
          api("/admin/v1/assignments"),
          api("/admin/v1/broker-runtime"),
          api("/admin/v1/broker-runtime/history?limit=12"),
          api("/admin/v1/broker-config", { headers: tokenHeaders(false) })
        ]);

        latestOffers = offers.offers || [];
        latestMembers = members.members || [];
        latestBackends = backends.backends || [];
        latestAssignments = assignments.assignments || [];
        latestRuntime = runtime;
        latestAssignmentCandidates = assignmentCandidates.candidates || [];
        latestAuditEvents = auditEvents.events || [];
        latestRuntimeHistory = runtimeHistory.items || [];
        syncAssignmentSelectors();
        void refreshAssignmentDraftState();
        renderAuditEvents(auditEvents.events || []);
        renderOffers(offers.offers || []);
        renderJoinRequests(joinRequests.join_requests || []);
        renderMembers(members.members || []);
        renderBackends(backends.backends || []);
        renderAssignmentCandidates(assignmentCandidates.candidates || []);
        renderAssignments(assignments.assignments || []);
        renderRuntime(runtime, brokerConfig);
        renderRuntimeHistory(runtimeHistory.items || []);
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
          '<div class="small">mode: ' + item.interaction_mode + '</div>' +
          '<div class="mono">price: ' + item.price.amount_wei + " / " + item.price.per_units + '</div>' +
          '<div class="row">' +
            '<button data-offer-promote="' + item.id + '" class="secondary">Use In Assignment Draft</button>' +
            '<button data-offer-edit="' + item.id + '" class="secondary">Load Into Editor</button>' +
            '<button data-offer-active="' + item.id + '" class="secondary">Set Active</button>' +
            '<button data-offer-disabled="' + item.id + '" class="secondary">Disable</button>' +
          '</div>'
        );
        host.appendChild(el);
      });
      host.querySelectorAll("[data-offer-promote]").forEach(btn => btn.onclick = () => promoteOfferToAssignmentDraft(btn.dataset.offerPromote));
      host.querySelectorAll("[data-offer-edit]").forEach(btn => btn.onclick = () => {
        const item = items.find(offer => offer.id === btn.dataset.offerEdit);
        if (item) loadOfferIntoForm(item);
      });
      host.querySelectorAll("[data-offer-active]").forEach(btn => btn.onclick = () => patchOfferStatus(btn.dataset.offerActive, "active"));
      host.querySelectorAll("[data-offer-disabled]").forEach(btn => btn.onclick = () => patchOfferStatus(btn.dataset.offerDisabled, "disabled"));
    }

    function renderJoinRequests(items) {
      const host = $("joinRequests");
      if (!host) return;
      host.innerHTML = "";
      items.forEach(item => {
        const el = card(
          "<strong>" + item.id + "</strong>" +
          '<div class="row"><span class="pill">' + item.status + '</span><span class="pill">' + (item.payout_mode || "onchain") + '</span></div>' +
          '<div class="mono">' + item.member_eth_address + '</div>' +
          '<div class="small">backends: ' + (item.requested_backends || []).length + '</div>' +
          ((item.requested_backends || []).map(b => '<div class="small">backend ' + b.id + ': ' + (b.verification_status || "unknown") + (b.verification_error ? " (" + b.verification_error + ")" : "") + '</div>').join("")) +
          '<div class="row">' +
            '<button data-preview-join="' + item.id + '" class="secondary">Preview</button>' +
            '<button data-refresh-join="' + item.id + '" class="secondary">Refresh Verification</button>' +
            '<button data-approve="' + item.id + '">Approve With Reason</button>' +
            '<button data-reject="' + item.id + '" class="secondary">Reject</button>' +
          '</div>'
        );
        host.appendChild(el);
      });
      host.querySelectorAll("[data-preview-join]").forEach(btn => btn.onclick = () => previewJoin(btn.dataset.previewJoin));
      host.querySelectorAll("[data-refresh-join]").forEach(btn => btn.onclick = () => refreshJoin(btn.dataset.refreshJoin));
      host.querySelectorAll("[data-approve]").forEach(btn => btn.onclick = () => reviewJoin(btn.dataset.approve, "approve"));
      host.querySelectorAll("[data-reject]").forEach(btn => btn.onclick = () => reviewJoin(btn.dataset.reject, "reject"));
    }

    function renderMembers(items) {
      const host = $("members");
      if (!host) return;
      host.innerHTML = "";
      items.forEach(item => {
        const transition = latestStatusTransition("member_status_updated", "member", item.id);
        const el = card(
          "<strong>" + (item.display_name || item.eth_address) + '</strong>' +
          '<div class="row"><span class="pill">' + (item.status || "active") + '</span></div>' +
          '<div class="mono">' + item.eth_address + '</div>' +
          '<div class="small">payout: ' + (item.payout_mode || "onchain") + '</div>' +
          (transition ? '<div class="small">last status change: ' + transitionSummary(transition) + '</div>' : '') +
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
      if (!host) return;
      host.innerHTML = "";
      items.forEach(item => {
        const transition = latestStatusTransition("member_backend_status_updated", "member_backend", item.id);
        const el = card(
          "<strong>" + item.id + '</strong>' +
          '<div class="row"><span class="pill">' + item.status + '</span><span class="pill">' + item.verification_status + '</span></div>' +
          '<div class="mono">' + item.url + '</div>' +
          '<div class="small">' + (item.verification_error || "") + '</div>' +
          (transition ? '<div class="small">last status change: ' + transitionSummary(transition) + '</div>' : '') +
          '<div class="row">' +
            '<button data-backend-verify="' + item.id + '" class="secondary">Verify</button>' +
            '<button data-backend-promote="' + item.id + '" class="secondary">Use In Assignment Draft</button>' +
            '<button data-backend-active="' + item.id + '" class="secondary">Set Active</button>' +
            '<button data-backend-draining="' + item.id + '" class="secondary">Drain</button>' +
            '<button data-backend-disabled="' + item.id + '" class="secondary">Disable</button>' +
          '</div>'
        );
        host.appendChild(el);
      });
      host.querySelectorAll("[data-backend-verify]").forEach(btn => btn.onclick = () => verifyBackend(btn.dataset.backendVerify));
      host.querySelectorAll("[data-backend-promote]").forEach(btn => btn.onclick = () => promoteBackendToAssignmentDraft(btn.dataset.backendPromote));
      host.querySelectorAll("[data-backend-active]").forEach(btn => btn.onclick = () => patchBackend(btn.dataset.backendActive, "active"));
      host.querySelectorAll("[data-backend-draining]").forEach(btn => btn.onclick = () => patchBackend(btn.dataset.backendDraining, "draining"));
      host.querySelectorAll("[data-backend-disabled]").forEach(btn => btn.onclick = () => patchBackend(btn.dataset.backendDisabled, "disabled"));
    }

    function renderAssignmentCandidates(items) {
      const host = $("assignmentCandidates");
      if (!host) return;
      host.innerHTML = "";
      items.forEach(item => {
        const claimsHtml = (item.suggested_claims || []).map(claim => {
          const suggested = (claim.suggested_offer_ids || [])[0] || "";
          const button = suggested
            ? '<button data-candidate-draft="' + item.backend_id + '|' + suggested + '" class="secondary">Use Suggested Offer</button>'
            : "";
          return (
            '<div class="check">' +
              '<strong class="' + (claim.servable ? "ok" : "warn") + '">claim</strong>' +
              '<div class="small">' + [claim.capability_id || "", claim.offering_id || "", claim.interaction_mode || ""].filter(Boolean).join(" / ") + '</div>' +
              '<div class="small">suggested_offers=' + ((claim.suggested_offer_ids || []).join(", ") || "none") + '</div>' +
              ((claim.suggestions || []).map(suggestion => '<div class="small">suggestion ' + (suggestion.offer_id || "") + ' score=' + String(suggestion.score || 0) + ' ' + (suggestion.reason || "") + '</div>').join("")) +
              (button ? '<div class="row">' + button + '</div>' : '') +
            '</div>'
          );
        }).join("");
        const el = card(
          "<strong>" + item.backend_id + "</strong>" +
          '<div class="row"><span class="pill">' + item.backend_status + '</span><span class="pill">' + item.verification_status + '</span><span class="pill">active_assignments=' + item.active_assignments + '</span></div>' +
          '<div class="small">' + (item.member_display_name || item.member_eth_address || item.member_id) + '</div>' +
          '<div class="small">total_assignments=' + item.assignment_count + '</div>' +
          claimsHtml
        );
        host.appendChild(el);
      });
      host.querySelectorAll("[data-candidate-draft]").forEach(btn => btn.onclick = () => {
        const parts = btn.dataset.candidateDraft.split("|");
        seedAssignmentDraft(parts[0] || "", parts[1] || "");
      });
    }

    function renderAssignments(items) {
      const host = $("assignments");
      if (!host) return;
      host.innerHTML = "";
      items.forEach(item => {
        const transition = latestStatusTransition("assignment_status_updated", "assignment", item.id);
        const el = card(
          "<strong>" + item.id + '</strong>' +
          '<div class="row"><span class="pill">' + item.status + '</span></div>' +
          '<div class="small">offer: ' + item.offer_id + '</div>' +
          '<div class="small">backend: ' + item.member_backend_id + '</div>' +
          (transition ? '<div class="small">last status change: ' + transitionSummary(transition) + '</div>' : '') +
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

    function renderRuntime(item, yaml) {
      const host = $("runtime");
      if (!host) return;
      host.innerHTML = "";
      const startedAt = item.last_apply_started_at || "";
      const finishedAt = item.last_apply_finished_at || "";
      const applyError = item.last_apply_error || "";
      host.appendChild(card(
        "<strong>Desired vs Applied</strong>" +
        '<div class="row"><span class="pill">' + (item.dirty ? "dirty" : "converged") + '</span><span class="pill">' + (item.broker_dirty ? "broker-dirty" : "broker-confirmed") + '</span><span class="pill">' + (item.last_apply_status || "unapplied") + '</span></div>' +
        '<div class="small">apply mode: ' + (item.apply_mode || "controller-refresh") + (item.apply_timeout_ms ? ' | timeout_ms=' + item.apply_timeout_ms : '') + '</div>' +
        '<div class="small">apply command configured: ' + String(!!item.apply_command_configured) + ' | broker admin configured: ' + String(!!item.broker_admin_configured) + '</div>' +
        '<div class="small">desired: <span class="mono">' + (item.desired_revision || "") + '</span></div>' +
        '<div class="small">applied: <span class="mono">' + (item.applied_revision || "") + '</span></div>' +
        (item.broker_reload_attempt_id ? '<div class="small">broker attempt: <span class="mono">' + item.broker_reload_attempt_id + '</span></div>' : '') +
        '<div class="small">broker loaded: <span class="mono">' + (item.broker_loaded_revision || "") + '</span></div>' +
        '<div class="small">offers: ' + item.offer_count + ', members: ' + item.member_count + ', backends: ' + item.backend_count + ', assignments: ' + item.assignment_count + '</div>' +
        (startedAt ? '<div class="small">last started: <span class="mono">' + startedAt + '</span></div>' : '') +
        (finishedAt ? '<div class="small">last finished: <span class="mono">' + finishedAt + '</span></div>' : '') +
        (item.broker_loaded_at ? '<div class="small">broker loaded at: <span class="mono">' + item.broker_loaded_at + '</span></div>' : '') +
        (item.broker_reload_status ? '<div class="small">broker reload status: ' + item.broker_reload_status + '</div>' : '') +
        (item.broker_reload_error ? '<div class="small bad">broker reload error: ' + item.broker_reload_error + '</div>' : '') +
        (applyError ? '<div class="small bad">last error: ' + applyError + '</div>' : '')
      ));
      if ($("runtimeYaml")) $("runtimeYaml").textContent = yaml;
    }

    function renderRuntimeHistory(items) {
      const host = $("runtimeHistory");
      if (!host) return;
      host.innerHTML = "";
      items.forEach(item => {
        const el = card(
          "<strong>" + (item.kind || item.status || "runtime_event") + "</strong>" +
          '<div class="row"><span class="pill">' + (item.status || "unknown") + '</span>' +
          (item.broker_reload_status ? '<span class="pill">' + item.broker_reload_status + '</span>' : '') +
          '</div>' +
          '<div class="small">' + (item.occurred_at || "") + (item.actor ? ' | actor=' + item.actor : '') + '</div>' +
          (item.desired_revision ? '<div class="small">desired: <span class="mono">' + item.desired_revision + '</span></div>' : '') +
          (item.current_revision ? '<div class="small">current: <span class="mono">' + item.current_revision + '</span></div>' : '') +
          (item.applied_revision ? '<div class="small">applied: <span class="mono">' + item.applied_revision + '</span></div>' : '') +
          (item.broker_reload_attempt_id ? '<div class="small">broker attempt: <span class="mono">' + item.broker_reload_attempt_id + '</span></div>' : '') +
          (item.broker_loaded_revision ? '<div class="small">broker loaded: <span class="mono">' + item.broker_loaded_revision + '</span></div>' : '') +
          (item.error ? '<div class="small bad">error: ' + item.error + '</div>' : '') +
          (item.broker_reload_error ? '<div class="small bad">broker reload error: ' + item.broker_reload_error + '</div>' : '')
        );
        host.appendChild(el);
      });
    }

    async function submitJSON(path, payload, method = "POST") {
      await api(path, { method, body: payload });
      await refreshAll();
    }

    async function reviewJoin(id, action) {
      try {
        setStatus("Submitting join-request review...");
        if (action === "approve") {
          const preview = await api("/admin/v1/join-request-preview", {
            method: "POST",
            body: JSON.stringify({ join_request_id: id })
          });
          renderJoinPreview(preview);
          if (!preview.approavable) {
            throw new Error((preview.reasons || []).join("; ") || "join request is not approvable");
          }
        }
        const payload = JSON.stringify({ reason: val("joinReviewReason") });
        await submitJSON("/admin/v1/join-requests/" + id + "/" + action, payload);
      } catch (err) {
        setStatus(err.message, "bad");
      }
    }

    async function previewJoin(id) {
      try {
        setStatus("Previewing join request...");
        const preview = await api("/admin/v1/join-request-preview", {
          method: "POST",
          body: JSON.stringify({ join_request_id: id })
        });
        renderJoinPreview(preview);
        setStatus("Join-request preview refreshed.", preview.approavable ? "ok" : "bad");
      } catch (err) {
        renderJoinPreview(null);
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

    on("refresh", "click", () => { void refreshAll(); });
    on("applyAuditFilters", "click", () => { void refreshAll(); });
    on("clearAuditFilters", "click", () => {
      if ($("auditKind")) $("auditKind").value = "";
      if ($("auditResourceType")) $("auditResourceType").value = "";
      if ($("auditResourceID")) $("auditResourceID").value = "";
      if ($("auditLimit")) $("auditLimit").value = "20";
      void refreshAll();
    });
    on("assignmentOfferSelect", "change", () => {
      if ($("assignmentOfferSelect").value) $("assignmentOfferId").value = $("assignmentOfferSelect").value;
      syncPayloadTextarea("assignmentPayload", assignmentPayloadFromForm());
      void refreshAssignmentDraftState();
    });
    on("assignmentBackendSelect", "change", () => {
      if ($("assignmentBackendSelect").value) $("assignmentBackendId").value = $("assignmentBackendSelect").value;
      syncPayloadTextarea("assignmentPayload", assignmentPayloadFromForm());
      void refreshAssignmentDraftState();
    });
    on("syncOfferPayload", "click", () => syncPayloadTextarea("offerPayload", offerPayloadFromForm()));
    on("syncJoinPayload", "click", () => syncPayloadTextarea("joinPayload", joinPayloadFromForm()));
    on("syncAssignmentPayload", "click", () => syncPayloadTextarea("assignmentPayload", assignmentPayloadFromForm()));
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
    on("submitJoin", "click", async () => {
      try {
        setStatus("Submitting join request...");
        await api("/member/v1/join-requests", {
          method: "POST",
          body: syncPayloadTextarea("joinPayload", joinPayloadFromForm()),
          headers: { "Content-Type": "application/json" }
        });
        await refreshAll();
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
    on("submitJoinRaw", "click", async () => {
      try {
        setStatus("Submitting raw join JSON...");
        await api("/member/v1/join-requests", {
          method: "POST",
          body: $("joinPayload").value,
          headers: { "Content-Type": "application/json" }
        });
        await refreshAll();
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
    on("createAssignment", "click", async () => {
      try {
        setStatus("Creating assignment...");
        await submitJSON("/admin/v1/assignments", syncPayloadTextarea("assignmentPayload", assignmentPayloadFromForm()));
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
    on("submitAssignmentRaw", "click", async () => {
      try {
        setStatus("Submitting raw assignment JSON...");
        await submitJSON("/admin/v1/assignments", $("assignmentPayload").value);
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
    on("applyRuntime", "click", async () => {
      try {
        setStatus("Applying desired runtime...");
        await submitJSON("/admin/v1/broker-runtime/apply", "{}");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
    on("markApplied", "click", async () => {
      try {
        setStatus("Marking desired revision applied...");
        await submitJSON("/admin/v1/broker-runtime/mark-applied", "{}");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
    on("markStarted", "click", async () => {
      try {
        setStatus("Marking apply started...");
        await submitJSON("/admin/v1/broker-runtime/mark-started", "{}");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });
    on("markFailed", "click", async () => {
      try {
        const error = window.prompt("Apply failure reason", "reload failed") || "";
        setStatus("Marking apply failed...");
        await submitJSON("/admin/v1/broker-runtime/mark-failed", JSON.stringify({ error }));
      } catch (err) {
        setStatus(err.message, "bad");
      }
    });

    if ($("offerId")) resetOfferForm();
    if ($("joinPayload")) syncPayloadTextarea("joinPayload", joinPayloadFromForm());
    if ($("assignmentPayload")) syncPayloadTextarea("assignmentPayload", assignmentPayloadFromForm());
    void refreshAssignmentDraftState();
    refreshAll();
