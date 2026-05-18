package adminpage

const pageScriptOpen = `
  <script>
`

const pageScript = `
    const $ = (id) => document.getElementById(id);
    const statusEl = $("status");
    let latestOffers = [];
    let latestBackends = [];
    let latestAssignmentPreview = null;
    let latestJoinPreview = null;

    function auditQuery() {
      const params = new URLSearchParams();
      if ($("auditKind").value.trim()) params.set("kind", $("auditKind").value.trim());
      if ($("auditResourceType").value.trim()) params.set("resource_type", $("auditResourceType").value.trim());
      if ($("auditResourceID").value.trim()) params.set("resource_id", $("auditResourceID").value.trim());
      if ($("auditLimit").value.trim()) params.set("limit", $("auditLimit").value.trim());
      const qs = params.toString();
      return qs ? "/admin/v1/audit-events?" + qs : "/admin/v1/audit-events";
    }

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
          const draftButton = (claim.active_offer_ids || []).length > 0
            ? '<div class="row"><button data-join-draft="' + (item.backend_id || "") + '|' + claim.active_offer_ids[0] + '" class="secondary">Use First Active Offer In Assignment Draft</button></div>'
            : "";
          claimDiv.innerHTML =
            '<strong class="' + (claim.servable ? "ok" : "warn") + '">claim</strong>' +
            '<div class="small">' + [claim.capability_id || "", claim.offering_id || "", claim.interaction_mode || ""].filter(Boolean).join(" / ") + '</div>' +
            '<div class="small">matching_offers=' + ((claim.matching_offer_ids || []).join(", ") || "none") + '</div>' +
            '<div class="small">active_offers=' + ((claim.active_offer_ids || []).join(", ") || "none") + '</div>' +
            draftButton +
            (claimReasons ? '<div class="small">' + claimReasons + '</div>' : '');
          host.appendChild(claimDiv);
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
      return latestOffers.find(item => item.id === $("assignmentOfferId").value.trim()) || null;
    }

    function selectedBackend() {
      return latestBackends.find(item => item.id === $("assignmentBackendId").value.trim()) || null;
    }

    async function refreshAssignmentDraftState() {
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

    async function refreshAll() {
      setStatus("Refreshing control-plane state...");
      try {
        const [auditEvents, offers, joinRequests, members, backends, assignments, runtime, brokerConfig] = await Promise.all([
          api(auditQuery()),
          api("/admin/v1/offers"),
          api("/admin/v1/join-requests"),
          api("/admin/v1/members"),
          api("/admin/v1/member-backends"),
          api("/admin/v1/assignments"),
          api("/admin/v1/broker-runtime"),
          api("/admin/v1/broker-config", { headers: tokenHeaders(false) })
        ]);

        latestOffers = offers.offers || [];
        latestBackends = backends.backends || [];
        syncAssignmentSelectors();
        void refreshAssignmentDraftState();
        renderAuditEvents(auditEvents.events || []);
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

    function renderAuditEvents(items) {
      const host = $("auditEvents");
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
        $("auditResourceType").value = parts[0] || "";
        $("auditResourceID").value = parts[1] || "";
        void refreshAll();
      });
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
        const payload = JSON.stringify({ reason: $("joinReviewReason").value.trim() });
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

    $("refresh").onclick = refreshAll;
    $("applyAuditFilters").onclick = () => { void refreshAll(); };
    $("clearAuditFilters").onclick = () => {
      $("auditKind").value = "";
      $("auditResourceType").value = "";
      $("auditResourceID").value = "";
      $("auditLimit").value = "20";
      void refreshAll();
    };
    $("assignmentOfferSelect").onchange = () => {
      if ($("assignmentOfferSelect").value) $("assignmentOfferId").value = $("assignmentOfferSelect").value;
      syncPayloadTextarea("assignmentPayload", assignmentPayloadFromForm());
      void refreshAssignmentDraftState();
    };
    $("assignmentBackendSelect").onchange = () => {
      if ($("assignmentBackendSelect").value) $("assignmentBackendId").value = $("assignmentBackendSelect").value;
      syncPayloadTextarea("assignmentPayload", assignmentPayloadFromForm());
      void refreshAssignmentDraftState();
    };
    $("syncOfferPayload").onclick = () => syncPayloadTextarea("offerPayload", offerPayloadFromForm());
    $("syncJoinPayload").onclick = () => syncPayloadTextarea("joinPayload", joinPayloadFromForm());
    $("syncAssignmentPayload").onclick = () => syncPayloadTextarea("assignmentPayload", assignmentPayloadFromForm());
    $("createOffer").onclick = async () => {
      try {
        setStatus("Creating offer...");
        await submitJSON("/admin/v1/offers", syncPayloadTextarea("offerPayload", offerPayloadFromForm()));
      } catch (err) {
        setStatus(err.message, "bad");
      }
    };
    $("updateOffer").onclick = async () => {
      try {
        const id = $("offerId").value.trim();
        if (!id) throw new Error("Offer ID is required");
        setStatus("Updating offer...");
        await submitJSON("/admin/v1/offers/" + encodeURIComponent(id), syncPayloadTextarea("offerPayload", offerPayloadFromForm()), "PATCH");
      } catch (err) {
        setStatus(err.message, "bad");
      }
    };
    $("resetOfferForm").onclick = () => resetOfferForm();
    $("submitOfferRaw").onclick = async () => {
      try {
        setStatus("Submitting raw offer JSON...");
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
          body: syncPayloadTextarea("joinPayload", joinPayloadFromForm()),
          headers: { "Content-Type": "application/json" }
        });
        await refreshAll();
      } catch (err) {
        setStatus(err.message, "bad");
      }
    };
    $("submitJoinRaw").onclick = async () => {
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
    };
    $("createAssignment").onclick = async () => {
      try {
        setStatus("Creating assignment...");
        await submitJSON("/admin/v1/assignments", syncPayloadTextarea("assignmentPayload", assignmentPayloadFromForm()));
      } catch (err) {
        setStatus(err.message, "bad");
      }
    };
    $("submitAssignmentRaw").onclick = async () => {
      try {
        setStatus("Submitting raw assignment JSON...");
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

    resetOfferForm();
    syncPayloadTextarea("joinPayload", joinPayloadFromForm());
    syncPayloadTextarea("assignmentPayload", assignmentPayloadFromForm());
    void refreshAssignmentDraftState();
    refreshAll();
`

const pageEnd = `
  </script>
</body>
</html>
`
