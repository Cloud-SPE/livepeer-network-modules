package adminpage

const pageBody = `<body>
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
        <h2>Offer Editor</h2>
        <div class="small" id="offerEditorState">Creating a new offer.</div>
        <div class="form-grid">
          <label>Offer ID
            <input id="offerId" value="rerank-zerank2">
          </label>
          <label>Capability ID
            <input id="offerCapability" value="rerank">
          </label>
          <label>Offering ID
            <input id="offerOffering" value="zerank-2-default">
          </label>
          <label>Interaction Mode
            <input id="offerInteraction" value="http-reqresp@v0">
          </label>
          <label>Work Unit
            <input id="offerWorkUnitName" value="requests">
          </label>
          <label>Extractor Type
            <input id="offerExtractorType" value="request-formula">
          </label>
          <label>Extractor Expression
            <input id="offerExtractorExpression" value="1">
          </label>
          <label>Amount Wei
            <input id="offerAmountWei" value="372000000000">
          </label>
          <label>Per Units
            <input id="offerPerUnits" type="number" min="1" value="1">
          </label>
        </div>
        <div class="actions">
          <button id="createOffer">Create Offer</button>
          <button id="updateOffer" class="secondary">Update Offer</button>
          <button id="resetOfferForm" class="secondary">Reset Form</button>
          <button id="syncOfferPayload" class="secondary">Refresh JSON</button>
        </div>
        <details>
          <summary>Advanced JSON</summary>
          <label>Offer JSON
            <textarea id="offerPayload"></textarea>
          </label>
          <button id="submitOfferRaw" class="secondary">Submit Raw JSON</button>
        </details>
      </div>

      <div class="panel">
        <h2>Submit Join Request</h2>
        <div class="form-grid">
          <label>Join Request ID
            <input id="joinId" value="join-sample">
          </label>
          <label>Member ETH Address
            <input id="joinMemberAddress" value="0xmember">
          </label>
          <label>Display Name
            <input id="joinDisplayName" value="member-a">
          </label>
          <label>Payout Mode
            <select id="joinPayoutMode">
              <option value="onchain" selected>onchain</option>
              <option value="manual">manual</option>
            </select>
          </label>
          <label>Backend ID
            <input id="joinBackendId" value="backend-sample">
          </label>
          <label>Backend Transport
            <input id="joinBackendTransport" value="http">
          </label>
          <label>Backend URL
            <input id="joinBackendUrl" value="http://backend:8080/v1/rerank">
          </label>
          <label>Health URL
            <input id="joinHealthUrl" value="http://backend:8080/healthz">
          </label>
          <label>Claimed Capability
            <input id="joinCapability" value="rerank">
          </label>
          <label>Claimed Offering
            <input id="joinOffering" value="zerank-2-default">
          </label>
          <label>Claimed Interaction
            <input id="joinInteraction" value="http-reqresp@v0">
          </label>
        </div>
        <div class="actions">
          <button id="submitJoin">Submit Join Request</button>
          <button id="syncJoinPayload" class="secondary">Refresh JSON</button>
        </div>
        <details>
          <summary>Advanced JSON</summary>
          <label>Join Request JSON
            <textarea id="joinPayload"></textarea>
          </label>
          <button id="submitJoinRaw" class="secondary">Submit Raw JSON</button>
        </details>
      </div>

      <div class="panel">
        <h2>Create Assignment</h2>
        <div class="small" id="assignmentDraftState">Select an offer and backend to draft an assignment.</div>
        <div id="assignmentPreviewDetails" class="check-list"></div>
        <div class="form-grid">
          <label>Assignment ID
            <input id="assignmentId" value="assign-sample">
          </label>
          <label>Offer ID
            <input id="assignmentOfferId" value="rerank-zerank2">
          </label>
          <label>Choose Offer
            <select id="assignmentOfferSelect">
              <option value="">Select an offer</option>
            </select>
          </label>
          <label>Member Backend ID
            <input id="assignmentBackendId" value="backend-sample">
          </label>
          <label>Choose Backend
            <select id="assignmentBackendSelect">
              <option value="">Select a backend</option>
            </select>
          </label>
        </div>
        <div class="actions">
          <button id="createAssignment">Create Assignment</button>
          <button id="syncAssignmentPayload" class="secondary">Refresh JSON</button>
        </div>
        <details>
          <summary>Advanced JSON</summary>
          <label>Assignment JSON
            <textarea id="assignmentPayload"></textarea>
          </label>
          <button id="submitAssignmentRaw" class="secondary">Submit Raw JSON</button>
        </details>
      </div>
    </section>

    <section class="grid" style="margin-top:18px">
      <div class="panel">
        <h2>Offers</h2>
        <div id="offers" class="card-list"></div>
      </div>
      <div class="panel">
        <h2>Join Requests</h2>
        <label>Review Reason
          <input id="joinReviewReason" placeholder="Optional reason for approval or rejection">
        </label>
        <div id="joinRequests" class="card-list"></div>
      </div>
      <div class="panel">
        <h2>Members</h2>
        <div id="members" class="card-list"></div>
      </div>
      <div class="panel">
        <h2>Backends</h2>
        <div class="small">Use verified backends to seed the assignment draft.</div>
        <div id="backends" class="card-list"></div>
      </div>
      <div class="panel">
        <h2>Assignments</h2>
        <div id="assignments" class="card-list"></div>
      </div>
      <div class="panel">
        <h2>Audit Events</h2>
        <div class="form-grid">
          <label>Kind
            <input id="auditKind" placeholder="offer_created">
          </label>
          <label>Resource Type
            <input id="auditResourceType" placeholder="offer">
          </label>
          <label>Resource ID
            <input id="auditResourceID" placeholder="rerank-zerank2">
          </label>
          <label>Limit
            <input id="auditLimit" type="number" min="1" value="20">
          </label>
        </div>
        <div class="actions">
          <button id="applyAuditFilters" class="secondary">Apply Filters</button>
          <button id="clearAuditFilters" class="secondary">Clear Filters</button>
        </div>
        <div id="auditEvents" class="card-list"></div>
      </div>
      <div class="panel">
        <h2>Broker Runtime</h2>
        <div id="runtime" class="card-list"></div>
        <pre id="runtimeYaml"></pre>
      </div>
    </section>
  </div>
`
