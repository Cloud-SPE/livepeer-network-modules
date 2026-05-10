import '@livepeer-rewrite/customer-portal-shared';
import { html, render } from 'lit';

type Customer = {
  id: string;
  email: string;
  tier: string;
  status: string;
  balance_usd_cents: string;
  reserved_usd_cents: string;
};

type AuditEvent = {
  id: string;
  actor: string;
  action: string;
  targetId: string | null;
  statusCode: number;
  occurredAt: string;
};

type Topup = {
  id: string;
  customer_id: string;
  stripe_session_id: string;
  amount_usd_cents: string;
  status: string;
  created_at: string;
  disputed_at: string | null;
  refunded_at: string | null;
};

type Reservation = {
  id: string;
  customer_id: string;
  work_id: string;
  kind: string;
  state: string;
  capability: string | null;
  model: string | null;
  amount_usd_cents: string | null;
  committed_usd_cents: string | null;
  refunded_usd_cents: string | null;
  amount_tokens: string | null;
  committed_tokens: string | null;
  refunded_tokens: string | null;
  created_at: string;
  resolved_at: string | null;
};

type ResolverCandidate = {
  brokerUrl: string;
  capability: string;
  offering: string;
  model: string | null;
  ethAddress: string;
  pricePerWorkUnitWei: string;
  workUnit: string;
  extra: unknown;
  constraints: unknown;
};

type CredentialSummary = {
  id: string;
  label: string | null;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
};

type HealthSnapshot = {
  status: string;
  checkedAt: string;
};

type PricingTier = 'starter' | 'standard' | 'pro' | 'premium';
type ImageQuality = 'standard' | 'hd';
type ChatTierEntry = {
  tier: PricingTier;
  inputUsdPerMillion: number;
  outputUsdPerMillion: number;
};
type ChatModelEntry = {
  modelOrPattern: string;
  isPattern: boolean;
  tier: PricingTier;
  sortOrder: number;
};
type EmbeddingsEntry = {
  modelOrPattern: string;
  isPattern: boolean;
  usdPerMillionTokens: number;
  sortOrder: number;
};
type AudioSpeechEntry = {
  modelOrPattern: string;
  isPattern: boolean;
  usdPerMillionChars: number;
  sortOrder: number;
};
type AudioTranscriptEntry = {
  modelOrPattern: string;
  isPattern: boolean;
  usdPerMinute: number;
  sortOrder: number;
};
type ImagesEntry = {
  modelOrPattern: string;
  isPattern: boolean;
  size: string;
  quality: ImageQuality;
  usdPerImage: number;
  sortOrder: number;
};
type RateCardSnapshot = {
  chatTiers: ChatTierEntry[];
  chatModels: ChatModelEntry[];
  embeddings: EmbeddingsEntry[];
  audioSpeech: AudioSpeechEntry[];
  audioTranscripts: AudioTranscriptEntry[];
  images: ImagesEntry[];
};
type ToastVariant = 'info' | 'success' | 'warning' | 'danger';

type AdminTopRoute = 'health' | 'nodes' | 'customers' | 'reservations' | 'topups' | 'rate-card' | 'audit';

const ADMIN_ROUTES: readonly AdminTopRoute[] = ['health', 'nodes', 'customers', 'reservations', 'topups', 'rate-card', 'audit'];
const PRICING_TIERS: readonly PricingTier[] = ['starter', 'standard', 'pro', 'premium'];
const IMAGE_SIZES = ['1024x1024', '1024x1792', '1792x1024'] as const;
const IMAGE_QUALITIES: readonly ImageQuality[] = ['standard', 'hd'];

const rootEl = document.getElementById('app');
if (!rootEl) throw new Error('missing #app');

installStyles();

const state = {
  authToken: sessionStorage.getItem('openai-gateway:admin-token') ?? '',
  actor: sessionStorage.getItem('openai-gateway:admin-actor') ?? '',
  customers: [] as Customer[],
  selected: null as Customer | null,
  selectedAuthTokens: [] as CredentialSummary[],
  selectedApiKeys: [] as CredentialSummary[],
  createdAuthToken: '',
  audit: [] as AuditEvent[],
  topups: [] as Topup[],
  reservations: [] as Reservation[],
  selectedReservation: null as Reservation | null,
  resolverCandidates: [] as ResolverCandidate[],
  health: null as HealthSnapshot | null,
  notice: null as { variant: ToastVariant; message: string } | null,
  pending: [] as string[],
  query: '',
  route: resolveRoute(),
  error: '',
  createEmail: '',
  createTier: 'free',
  adjustDelta: '',
  adjustReason: '',
  refundSessionId: '',
  refundReason: '',
  rateCard: emptyRateCard(),
};

window.addEventListener('hashchange', () => {
  state.route = resolveRoute();
  draw();
});

void bootstrap();

async function bootstrap(): Promise<void> {
  if (!location.hash) {
    setRoute('health');
  }
  if (state.authToken && state.actor) {
    await refresh().catch(() => undefined);
  }
  draw();
}

function draw(): void {
  render(
    html`
      <portal-layout brand="OpenAI Gateway Admin">
        ${state.authToken && state.actor ? navView() : ''}
        ${state.authToken && state.actor ? shellView() : loginView()}
        <span slot="footer">Operator console</span>
      </portal-layout>
    `,
    rootEl!,
  );
}

function navView() {
  return html`
    <nav slot="nav" class="openai-admin-nav" aria-label="Primary">
      ${ADMIN_ROUTES.map((route) => navButton(route, labelForRoute(route)))}
      <portal-button variant="ghost" ?loading=${isBusy('logout')} @click=${logout}>Sign out</portal-button>
    </nav>
  `;
}

function shellView() {
  return html`
    <div class="openai-admin-shell">
      <portal-card heading=${labelForRoute(currentTopRoute())} subheading=${pageSubheading()}>
        <div class="openai-admin-stack--sm">
          <div class="openai-admin-session">
            <div class="openai-admin-session-meta">
              <div class="openai-admin-eyebrow">Operator session</div>
              <div class="openai-admin-session-value">${state.actor}</div>
            </div>
            ${state.pending.length
              ? html`<portal-toast variant="info" .message=${pendingSummary()}></portal-toast>`
              : html``}
          </div>
          ${state.notice ? html`<portal-toast .variant=${state.notice.variant} .message=${state.notice.message}></portal-toast>` : ''}
          ${state.error ? html`<portal-toast variant="danger" .message=${state.error}></portal-toast>` : ''}
        </div>
      </portal-card>
      ${routeView()}
    </div>
  `;
}

function loginView() {
  return html`
    <portal-card heading="Admin login" subheading="Use the gateway admin token and actor identity to access operator workflows.">
      <form @submit=${login} class="openai-admin-login-form">
        <portal-input name="token" label="Admin token" required></portal-input>
        <portal-input name="actor" label="Actor" required></portal-input>
        <portal-action-row>
          <portal-button type="submit" ?loading=${isBusy('login')}>Sign in</portal-button>
        </portal-action-row>
      </form>
    </portal-card>
    ${state.error ? html`<portal-toast variant="danger" .message=${state.error}></portal-toast>` : ''}
  `;
}

function routeView() {
  const route = state.route;
  const parts = route.split('/');
  const head = parts[0] as AdminTopRoute;
  switch (head) {
    case 'nodes':
      return nodesView(parts[1] ? decodeURIComponent(parts[1]) : null);
    case 'customers':
      return customersView(parts[1] ? decodeURIComponent(parts[1]) : null);
    case 'reservations':
      return reservationsView();
    case 'topups':
      return topupsView();
    case 'rate-card':
      return rateCardView();
    case 'audit':
      return auditView();
    case 'health':
    default:
      return healthView();
  }
}

function healthView() {
  return html`
    <div class="openai-admin-metrics">
      <portal-metric-tile label="Gateway health" value=${state.health?.status ?? 'unknown'}></portal-metric-tile>
      <portal-metric-tile label="Customers" value=${String(state.customers.length)}></portal-metric-tile>
      <portal-metric-tile label="Reservations" value=${String(state.reservations.length)}></portal-metric-tile>
      <portal-metric-tile label="Resolver candidates" value=${String(state.resolverCandidates.length)}></portal-metric-tile>
    </div>
    <portal-card heading="Health summary" subheading="Operational summary across the OpenAI gateway control plane.">
      ${metaList([
        ['Gateway endpoint', state.health?.status ?? 'unavailable'],
        ['Checked', state.health?.checkedAt ?? '—'],
        ['Latest top-ups tracked', String(state.topups.length)],
        ['Latest admin audit events', String(state.audit.length)],
      ])}
    </portal-card>
  `;
}

function nodesView(selectedBrokerUrl: string | null) {
  const selectedNode =
    selectedBrokerUrl === null
      ? null
      : state.resolverCandidates.find((row) => row.brokerUrl === selectedBrokerUrl) ?? null;
  return html`
    <div class="openai-admin-stack">
      <portal-data-table heading="Nodes" description="Resolver candidates currently visible to the OpenAI gateway.">
        <table>
          <thead>
            <tr><th>Capability</th><th>Model</th><th>Offering</th><th>Broker URL</th><th></th></tr>
          </thead>
          <tbody>
            ${state.resolverCandidates.map(
              (row) => html`<tr>
                <td>${row.capability || 'static-broker'}</td>
                <td>${row.model ?? '—'}</td>
                <td>${row.offering || 'default'}</td>
                <td>${row.brokerUrl}</td>
                <td><portal-button variant="ghost" @click=${() => setRoute(`nodes/${encodeURIComponent(row.brokerUrl)}`)}>Open</portal-button></td>
              </tr>`,
            )}
          </tbody>
        </table>
      </portal-data-table>
      <portal-card heading="Node detail" subheading="Selected resolver candidate route metadata.">
        ${selectedNode
          ? html`
              ${metaList([
                ['Broker URL', selectedNode.brokerUrl],
                ['Capability', selectedNode.capability || 'static-broker'],
                ['Model', selectedNode.model ?? '—'],
                ['Offering', selectedNode.offering || 'default'],
                ['Orchestrator', selectedNode.ethAddress || '—'],
                ['Price per work unit', selectedNode.pricePerWorkUnitWei],
                ['Work unit', selectedNode.workUnit],
              ])}
              <pre>${prettyJson(selectedNode.extra)}</pre>
              <pre>${prettyJson(selectedNode.constraints)}</pre>
            `
          : html`<p class="openai-admin-empty">Select a node to inspect routing metadata.</p>`}
      </portal-card>
    </div>
  `;
}

function prettyJson(value: unknown): string {
  return JSON.stringify(value ?? null, null, 2);
}

function customersView(selectedCustomerId: string | null) {
  const selected = selectedCustomerId
    ? state.customers.find((row) => row.id === selectedCustomerId) ?? state.selected
    : state.selected;
  return html`
    <div class="openai-admin-stack">
      <portal-card heading="Customers" subheading="Search, create, and select customer accounts.">
        <form @submit=${searchCustomers} class="openai-admin-form-inline">
          <portal-input name="q" label="Search" .value=${state.query}></portal-input>
          <portal-button type="submit" ?loading=${isBusy('searchCustomers')}>Search</portal-button>
        </form>
        <portal-detail-section heading="Create customer" description="Provision a new customer and issue the initial UI auth token.">
          <form @submit=${createCustomer} class="openai-admin-form-stack">
            <portal-input
              name="email"
              label="Customer email"
              .value=${state.createEmail}
              @portal-input-change=${(e: CustomEvent<{ value: string }>) => {
                state.createEmail = e.detail.value;
              }}
            ></portal-input>
            <label class="openai-admin-field">
              Tier
              <select
                class="openai-admin-select"
                name="tier"
                .value=${state.createTier}
                @change=${(e: Event) => {
                  state.createTier = (e.currentTarget as HTMLSelectElement).value;
                }}
              >
                <option value="free">free</option>
                <option value="prepaid">prepaid</option>
              </select>
            </label>
            <portal-button type="submit" ?loading=${isBusy('createCustomer')}>Create customer</portal-button>
          </form>
          ${state.createdAuthToken
            ? html`<div class="openai-admin-section-gap"><portal-toast variant="success" message=${`Initial UI auth token: ${state.createdAuthToken}`}></portal-toast></div>`
            : ''}
        </portal-detail-section>
      </portal-card>
      <portal-data-table heading="Customer list" description="Search results and provisioned customer accounts.">
        <table>
          <thead>
            <tr><th>Email</th><th>Tier</th><th>Status</th><th>Balance</th><th>Reserved</th><th></th></tr>
          </thead>
          <tbody>
            ${state.customers.map(
              (row) => html`<tr>
                <td>${row.email}</td>
                <td>${row.tier}</td>
                <td><portal-status-pill label=${row.status}></portal-status-pill></td>
                <td>${usd(row.balance_usd_cents)}</td>
                <td>${usd(row.reserved_usd_cents)}</td>
                <td><portal-button variant="ghost" @click=${() => setRoute(`customers/${row.id}`)}>Open</portal-button></td>
              </tr>`,
            )}
          </tbody>
        </table>
      </portal-data-table>
      <portal-card heading="Customer detail" subheading="Balance, status, auth tokens, and API keys for the selected account.">
        ${selected ? customerDetailView(selected) : html`<p class="openai-admin-empty">Select a customer to inspect details.</p>`}
      </portal-card>
    </div>
  `;
}

function customerDetailView(customer: Customer) {
  return html`
    <div class="openai-admin-stack">
      ${metaList([
        ['ID', customer.id],
        ['Email', customer.email],
        ['Tier', customer.tier],
        ['Status', html`<portal-status-pill label=${customer.status}></portal-status-pill>`],
        ['Balance', usd(customer.balance_usd_cents)],
        ['Reserved', usd(customer.reserved_usd_cents)],
      ])}
      <portal-detail-section heading="Balance adjustment" description="Apply a manual debit or credit with an audit reason.">
        <form @submit=${adjustBalance} class="openai-admin-form-stack">
          <portal-input
            name="delta"
            label="Delta USD cents"
            placeholder="500 or -500"
            .value=${state.adjustDelta}
            @portal-input-change=${(e: CustomEvent<{ value: string }>) => {
              state.adjustDelta = e.detail.value;
            }}
          ></portal-input>
          <portal-input
            name="reason"
            label="Reason"
            .value=${state.adjustReason}
            @portal-input-change=${(e: CustomEvent<{ value: string }>) => {
              state.adjustReason = e.detail.value;
            }}
          ></portal-input>
          <portal-button type="submit" ?loading=${isBusy('adjustBalance')}>Apply balance change</portal-button>
        </form>
      </portal-detail-section>
      <portal-detail-section heading="Account status" description="Suspend, reactivate, or close the customer.">
        <portal-action-row>
          <portal-button variant="ghost" ?loading=${isBusy('setStatus:active')} @click=${() => setStatus('active')}>Set active</portal-button>
          <portal-button variant="ghost" ?loading=${isBusy('setStatus:suspended')} @click=${() => setStatus('suspended')}>Suspend</portal-button>
          <portal-button variant="danger" ?loading=${isBusy('setStatus:closed')} @click=${() => setStatus('closed')}>Close</portal-button>
        </portal-action-row>
      </portal-detail-section>
      <portal-detail-section heading="Refund top-up" description="Reverse a previously credited Stripe checkout session.">
        <form @submit=${refundTopup} class="openai-admin-form-stack">
          <portal-input
            name="stripe_session_id"
            label="Stripe session ID"
            .value=${state.refundSessionId}
            @portal-input-change=${(e: CustomEvent<{ value: string }>) => {
              state.refundSessionId = e.detail.value;
            }}
          ></portal-input>
          <portal-input
            name="reason"
            label="Reason"
            .value=${state.refundReason}
            @portal-input-change=${(e: CustomEvent<{ value: string }>) => {
              state.refundReason = e.detail.value;
            }}
          ></portal-input>
          <portal-button variant="danger" type="submit" ?loading=${isBusy('refundTopup')}>Refund top-up</portal-button>
        </form>
      </portal-detail-section>
      <div class="openai-admin-card-grid">
        <portal-card heading="UI auth tokens" subheading="Customer portal login tokens.">
          <portal-api-keys
            .keys=${serializeCredentials(state.selectedAuthTokens)}
            @portal-api-key-issue=${issueCustomerAuthToken}
            @portal-api-key-revoke=${revokeCustomerAuthToken}
          ></portal-api-keys>
        </portal-card>
        <portal-card heading="API keys" subheading="Product API credentials for this customer.">
          <portal-api-keys
            .keys=${serializeCredentials(state.selectedApiKeys)}
            @portal-api-key-issue=${issueCustomerApiKey}
            @portal-api-key-revoke=${revokeCustomerApiKey}
          ></portal-api-keys>
        </portal-card>
      </div>
      <portal-detail-section heading="Customer request ledger" description="Latest reservations and settled usage for this customer.">
        <portal-data-table heading="Reservations" description="Reserved, committed, and refunded work for the selected customer.">
          <table>
            <thead>
              <tr><th>Created</th><th>Capability</th><th>Model</th><th>Status</th><th>Committed</th><th>Work ID</th></tr>
            </thead>
            <tbody>
              ${state.reservations
                .filter((row) => row.customer_id === customer.id)
                .map(
                  (row) => html`<tr>
                    <td>${row.created_at}</td>
                    <td>${row.capability ?? row.kind}</td>
                    <td>${row.model ?? 'n/a'}</td>
                    <td><portal-status-pill label=${row.state}></portal-status-pill></td>
                    <td>${formatReservationValue(row.committed_usd_cents, row.committed_tokens)}</td>
                    <td>${row.work_id}</td>
                    <td><portal-button variant="ghost" @click=${() => openReservation(row.id)}>Open</portal-button></td>
                  </tr>`,
                )}
            </tbody>
          </table>
        </portal-data-table>
        ${state.selectedReservation && state.selectedReservation.customer_id === customer.id
          ? html`
              <div class="openai-admin-section-gap">
                <portal-detail-section heading="Selected request" description="Detailed reservation record for the chosen customer request.">
                  ${metaList(
                    [
                      ['Reservation', state.selectedReservation.id],
                      ['Work ID', state.selectedReservation.work_id],
                      ['Capability', state.selectedReservation.capability ?? state.selectedReservation.kind],
                      ['Model', state.selectedReservation.model ?? 'n/a'],
                      ['Status', html`<portal-status-pill label=${state.selectedReservation.state}></portal-status-pill>`],
                      ['Reserved', formatReservationValue(state.selectedReservation.amount_usd_cents, state.selectedReservation.amount_tokens)],
                      ['Committed', formatReservationValue(state.selectedReservation.committed_usd_cents, state.selectedReservation.committed_tokens)],
                      ['Refunded', formatReservationValue(state.selectedReservation.refunded_usd_cents, state.selectedReservation.refunded_tokens)],
                      ['Created', state.selectedReservation.created_at],
                      ['Resolved', state.selectedReservation.resolved_at ?? '—'],
                    ],
                    'openai-admin-meta-list openai-admin-meta-list--tight',
                  )}
                </portal-detail-section>
              </div>
            `
          : ''}
      </portal-detail-section>
    </div>
  `;
}

function reservationsView() {
  return html`
    <portal-data-table heading="Reservations" description="Gateway reservation ledger entries across all customers.">
      <table>
        <thead>
          <tr><th>Created</th><th>Customer</th><th>Capability</th><th>Model</th><th>Status</th><th>Reserved</th><th>Committed</th><th></th></tr>
        </thead>
        <tbody>
          ${state.reservations.map(
            (row) => html`<tr>
              <td>${row.created_at}</td>
              <td>${row.customer_id}</td>
              <td>${row.capability ?? row.kind}</td>
              <td>${row.model ?? 'n/a'}</td>
              <td><portal-status-pill label=${row.state}></portal-status-pill></td>
              <td>${formatReservationValue(row.amount_usd_cents, row.amount_tokens)}</td>
              <td>${formatReservationValue(row.committed_usd_cents, row.committed_tokens)}</td>
              <td><portal-button variant="ghost" @click=${() => openReservation(row.id)}>Open</portal-button></td>
            </tr>`,
          )}
        </tbody>
      </table>
    </portal-data-table>
  `;
}

function topupsView() {
  return html`
      <portal-data-table heading="Top-ups" description="Latest credited and refunded balance events.">
      <table>
        <thead>
          <tr><th>Created</th><th>Customer</th><th>Amount</th><th>Status</th><th>Stripe session</th></tr>
        </thead>
        <tbody>
          ${state.topups.map(
            (row) => html`<tr>
              <td>${row.created_at}</td>
              <td>${row.customer_id}</td>
              <td>${usd(row.amount_usd_cents)}</td>
              <td><portal-status-pill label=${row.status}></portal-status-pill></td>
              <td>${row.stripe_session_id}</td>
            </tr>`,
          )}
        </tbody>
      </table>
    </portal-data-table>
  `;
}

function rateCardView() {
  return html`
    <div class="openai-admin-stack">
      <portal-card heading="Rate card management" subheading="Edit customer pricing by surface instead of replacing raw snapshot JSON.">
        <portal-action-row align="end">
          <portal-button variant="ghost" ?loading=${isBusy('loadRateCard')} @click=${loadRateCard}>Reload snapshot</portal-button>
          <portal-button ?loading=${isBusy('saveRateCard')} @click=${saveRateCard}>Save rate card</portal-button>
        </portal-action-row>
      </portal-card>
      <portal-data-table heading="Chat tiers" description="Per-tier input and output pricing in USD per million tokens.">
        <table>
          <thead>
            <tr><th>Tier</th><th>Input USD / 1M</th><th>Output USD / 1M</th></tr>
          </thead>
          <tbody>
            ${PRICING_TIERS.map((tier) => {
              const row = state.rateCard.chatTiers.find((entry) => entry.tier === tier) ?? {
                tier,
                inputUsdPerMillion: 0,
                outputUsdPerMillion: 0,
              };
              return html`<tr>
                <td>${tier}</td>
                <td>${numberCell(row.inputUsdPerMillion, (value) => setChatTierPrice(tier, 'inputUsdPerMillion', value))}</td>
                <td>${numberCell(row.outputUsdPerMillion, (value) => setChatTierPrice(tier, 'outputUsdPerMillion', value))}</td>
              </tr>`;
            })}
          </tbody>
        </table>
      </portal-data-table>
      ${pricingEntriesTable({
        heading: 'Chat model mapping',
        description: 'Map exact models or glob patterns onto pricing tiers.',
        entries: state.rateCard.chatModels,
        columns: ['Model / pattern', 'Match', 'Tier', 'Sort', ''],
        renderRow: (entry, index) => html`<tr>
          <td>${textCell(entry.modelOrPattern, (value) => updateChatModel(index, 'modelOrPattern', value))}</td>
          <td>${boolCell(entry.isPattern, (value) => updateChatModel(index, 'isPattern', value))}</td>
          <td>${tierCell(entry.tier, (value) => updateChatModel(index, 'tier', value))}</td>
          <td>${numberCell(entry.sortOrder, (value) => updateChatModel(index, 'sortOrder', value))}</td>
          <td>${deleteCell(() => removeRateCardRow('chatModels', index))}</td>
        </tr>`,
        addRow: () => addRateCardRow('chatModels'),
      })}
      ${pricingEntriesTable({
        heading: 'Embeddings',
        description: 'USD per million tokens for each embeddings selector.',
        entries: state.rateCard.embeddings,
        columns: ['Model / pattern', 'Match', 'USD / 1M tokens', 'Sort', ''],
        renderRow: (entry, index) => html`<tr>
          <td>${textCell(entry.modelOrPattern, (value) => updateEmbeddings(index, 'modelOrPattern', value))}</td>
          <td>${boolCell(entry.isPattern, (value) => updateEmbeddings(index, 'isPattern', value))}</td>
          <td>${numberCell(entry.usdPerMillionTokens, (value) => updateEmbeddings(index, 'usdPerMillionTokens', value))}</td>
          <td>${numberCell(entry.sortOrder, (value) => updateEmbeddings(index, 'sortOrder', value))}</td>
          <td>${deleteCell(() => removeRateCardRow('embeddings', index))}</td>
        </tr>`,
        addRow: () => addRateCardRow('embeddings'),
      })}
      ${pricingEntriesTable({
        heading: 'Images',
        description: 'Composite pricing by model selector, size, and quality.',
        entries: state.rateCard.images,
        columns: ['Model / pattern', 'Match', 'Size', 'Quality', 'USD / image', 'Sort', ''],
        renderRow: (entry, index) => html`<tr>
          <td>${textCell(entry.modelOrPattern, (value) => updateImages(index, 'modelOrPattern', value))}</td>
          <td>${boolCell(entry.isPattern, (value) => updateImages(index, 'isPattern', value))}</td>
          <td>${selectCell(entry.size, IMAGE_SIZES, (value) => updateImages(index, 'size', value))}</td>
          <td>${selectCell(entry.quality, IMAGE_QUALITIES, (value) => updateImages(index, 'quality', value as ImageQuality))}</td>
          <td>${numberCell(entry.usdPerImage, (value) => updateImages(index, 'usdPerImage', value))}</td>
          <td>${numberCell(entry.sortOrder, (value) => updateImages(index, 'sortOrder', value))}</td>
          <td>${deleteCell(() => removeRateCardRow('images', index))}</td>
        </tr>`,
        addRow: () => addRateCardRow('images'),
      })}
      ${pricingEntriesTable({
        heading: 'Audio speech',
        description: 'USD per million characters for text-to-speech routes.',
        entries: state.rateCard.audioSpeech,
        columns: ['Model / pattern', 'Match', 'USD / 1M chars', 'Sort', ''],
        renderRow: (entry, index) => html`<tr>
          <td>${textCell(entry.modelOrPattern, (value) => updateAudioSpeech(index, 'modelOrPattern', value))}</td>
          <td>${boolCell(entry.isPattern, (value) => updateAudioSpeech(index, 'isPattern', value))}</td>
          <td>${numberCell(entry.usdPerMillionChars, (value) => updateAudioSpeech(index, 'usdPerMillionChars', value))}</td>
          <td>${numberCell(entry.sortOrder, (value) => updateAudioSpeech(index, 'sortOrder', value))}</td>
          <td>${deleteCell(() => removeRateCardRow('audioSpeech', index))}</td>
        </tr>`,
        addRow: () => addRateCardRow('audioSpeech'),
      })}
      ${pricingEntriesTable({
        heading: 'Audio transcriptions',
        description: 'USD per minute for transcription routes.',
        entries: state.rateCard.audioTranscripts,
        columns: ['Model / pattern', 'Match', 'USD / minute', 'Sort', ''],
        renderRow: (entry, index) => html`<tr>
          <td>${textCell(entry.modelOrPattern, (value) => updateAudioTranscripts(index, 'modelOrPattern', value))}</td>
          <td>${boolCell(entry.isPattern, (value) => updateAudioTranscripts(index, 'isPattern', value))}</td>
          <td>${numberCell(entry.usdPerMinute, (value) => updateAudioTranscripts(index, 'usdPerMinute', value))}</td>
          <td>${numberCell(entry.sortOrder, (value) => updateAudioTranscripts(index, 'sortOrder', value))}</td>
          <td>${deleteCell(() => removeRateCardRow('audioTranscripts', index))}</td>
        </tr>`,
        addRow: () => addRateCardRow('audioTranscripts'),
      })}
    </div>
  `;
}

function auditView() {
  return html`
    <portal-data-table heading="Admin audit" description="Latest operator actions against the gateway account system.">
      <table>
        <thead>
          <tr><th>Time</th><th>Actor</th><th>Action</th><th>Target</th><th>Status</th></tr>
        </thead>
        <tbody>
          ${state.audit.map(
            (row) => html`<tr>
              <td>${row.occurredAt}</td>
              <td>${row.actor}</td>
              <td>${row.action}</td>
              <td>${row.targetId ?? '—'}</td>
              <td>${row.statusCode}</td>
            </tr>`,
          )}
        </tbody>
      </table>
    </portal-data-table>
  `;
}

function resolveRoute(): string {
  const raw = location.hash.replace(/^#/, '');
  const head = raw.split('/')[0] || 'health';
  return ADMIN_ROUTES.includes(head as AdminTopRoute) ? raw : 'health';
}

function setRoute(route: string): void {
  location.hash = route;
}

function navButton(route: AdminTopRoute, label: string) {
  return html`<portal-button variant=${currentTopRoute() === route ? 'primary' : 'ghost'} @click=${() => setRoute(route)}>${label}</portal-button>`;
}

function currentTopRoute(): AdminTopRoute {
  return state.route.split('/')[0] as AdminTopRoute;
}

function labelForRoute(route: AdminTopRoute): string {
  return route === 'rate-card' ? 'Rate Card' : route.charAt(0).toUpperCase() + route.slice(1);
}

async function login(event: Event): Promise<void> {
  event.preventDefault();
  const form = new FormData(event.currentTarget as HTMLFormElement);
  state.authToken = String(form.get('token') ?? '');
  state.actor = String(form.get('actor') ?? '');
  sessionStorage.setItem('openai-gateway:admin-token', state.authToken);
  sessionStorage.setItem('openai-gateway:admin-actor', state.actor);
  await withPending('login', async () => {
    await refresh();
    setNotice('success', 'Signed in to the OpenAI gateway admin console.');
  });
}

async function searchCustomers(event: Event): Promise<void> {
  event.preventDefault();
  const form = new FormData(event.currentTarget as HTMLFormElement);
  state.query = String(form.get('q') ?? '');
  await withPending('searchCustomers', async () => {
    const out = await adminRequest(`/admin/customers${state.query ? `?q=${encodeURIComponent(state.query)}` : ''}`);
    state.customers = out.customers;
    draw();
  });
}

async function createCustomer(event: Event): Promise<void> {
  event.preventDefault();
  await withPending('createCustomer', async () => {
    const out = await adminRequest('/admin/customers', {
      method: 'POST',
      body: JSON.stringify({
        email: state.createEmail,
        tier: state.createTier,
      }),
    });
    state.createdAuthToken = out.auth_token;
    state.createEmail = '';
    await refresh(out.customer.id);
    setRoute(`customers/${out.customer.id}`);
    setNotice('success', `Created customer ${out.customer.email}.`);
  });
}

async function selectCustomer(id: string): Promise<void> {
  const detail = await adminRequest(`/admin/customers/${encodeURIComponent(id)}`);
  state.selected = detail.customer;
  const [authTokens, apiKeys] = await Promise.all([
    adminRequest(`/admin/customers/${encodeURIComponent(id)}/auth-tokens`),
    adminRequest(`/admin/customers/${encodeURIComponent(id)}/api-keys`),
  ]);
  state.selectedAuthTokens = authTokens.auth_tokens;
  state.selectedApiKeys = apiKeys.api_keys;
}

async function loadRateCard(event?: Event): Promise<void> {
  event?.preventDefault();
  await withPending('loadRateCard', async () => {
    const snapshot = await adminRequest('/admin/openai/rate-card');
    state.rateCard = normalizeRateCard(snapshot);
    draw();
    setNotice('success', 'Reloaded the current rate card snapshot.');
  });
}

async function openReservation(id: string): Promise<void> {
  const out = await adminRequest(`/admin/reservations/${encodeURIComponent(id)}`);
  state.selectedReservation = out.reservation;
  draw();
}

async function adjustBalance(event: Event): Promise<void> {
  event.preventDefault();
  if (!state.selected) return;
  const customerId = state.selected.id;
  await withPending('adjustBalance', async () => {
    await adminRequest(`/admin/customers/${encodeURIComponent(customerId)}/balance`, {
      method: 'POST',
      body: JSON.stringify({
        delta_usd_cents: state.adjustDelta,
        reason: state.adjustReason,
      }),
    });
    state.adjustDelta = '';
    state.adjustReason = '';
    await refresh(customerId);
    setNotice('success', 'Applied the customer balance adjustment.');
  });
}

async function setStatus(status: 'active' | 'suspended' | 'closed'): Promise<void> {
  if (!state.selected) return;
  const customerId = state.selected.id;
  await withPending(`setStatus:${status}`, async () => {
    await adminRequest(`/admin/customers/${encodeURIComponent(customerId)}/status`, {
      method: 'POST',
      body: JSON.stringify({ status }),
    });
    await refresh(customerId);
    setNotice('success', `Customer status updated to ${status}.`);
  });
}

async function refundTopup(event: Event): Promise<void> {
  event.preventDefault();
  if (!state.selected) return;
  const customerId = state.selected.id;
  await withPending('refundTopup', async () => {
    await adminRequest(`/admin/customers/${encodeURIComponent(customerId)}/refund`, {
      method: 'POST',
      body: JSON.stringify({
        stripe_session_id: state.refundSessionId,
        reason: state.refundReason,
      }),
    });
    state.refundSessionId = '';
    state.refundReason = '';
    await refresh(customerId);
    setNotice('success', 'Refund request submitted for the selected Stripe top-up.');
  });
}

async function issueCustomerAuthToken(event: CustomEvent<{ label: string }>): Promise<void> {
  if (!state.selected) return;
  const customerId = state.selected.id;
  const widget = credentialWidgetFromEvent(event);
  await withPending('issueCustomerAuthToken', async () => {
    const out = await adminRequest(`/admin/customers/${encodeURIComponent(customerId)}/auth-tokens`, {
      method: 'POST',
      body: JSON.stringify({ label: event.detail.label || undefined }),
    });
    widget?.showPlaintext?.(out.auth_token);
    await refresh(customerId);
    setNotice('success', 'Issued a new customer UI auth token.');
  });
}

async function revokeCustomerAuthToken(event: CustomEvent<{ id: string }>): Promise<void> {
  if (!state.selected) return;
  const customerId = state.selected.id;
  await withPending('revokeCustomerAuthToken', async () => {
    await adminRequest(
      `/admin/customers/${encodeURIComponent(customerId)}/auth-tokens/${encodeURIComponent(event.detail.id)}`,
      { method: 'DELETE' },
    );
    await refresh(customerId);
    setNotice('success', 'Revoked the customer UI auth token.');
  });
}

async function issueCustomerApiKey(event: CustomEvent<{ label: string }>): Promise<void> {
  if (!state.selected) return;
  const customerId = state.selected.id;
  const widget = credentialWidgetFromEvent(event);
  await withPending('issueCustomerApiKey', async () => {
    const out = await adminRequest(`/admin/customers/${encodeURIComponent(customerId)}/api-keys`, {
      method: 'POST',
      body: JSON.stringify({ label: event.detail.label || undefined }),
    });
    widget?.showPlaintext?.(out.api_key);
    await refresh(customerId);
    setNotice('success', 'Issued a new customer API key.');
  });
}

async function revokeCustomerApiKey(event: CustomEvent<{ id: string }>): Promise<void> {
  if (!state.selected) return;
  const customerId = state.selected.id;
  await withPending('revokeCustomerApiKey', async () => {
    await adminRequest(
      `/admin/customers/${encodeURIComponent(customerId)}/api-keys/${encodeURIComponent(event.detail.id)}`,
      { method: 'DELETE' },
    );
    await refresh(customerId);
    setNotice('success', 'Revoked the customer API key.');
  });
}

async function saveRateCard(event?: Event): Promise<void> {
  event?.preventDefault();
  await withPending('saveRateCard', async () => {
    await adminRequest('/admin/openai/rate-card', {
      method: 'PUT',
      body: JSON.stringify(state.rateCard),
    });
    await refresh(state.selected?.id);
    setNotice('success', 'Saved the OpenAI gateway rate card.');
  });
}

async function refresh(preferredCustomerId?: string): Promise<void> {
  const [customers, audit, topups, reservations, resolverCandidates, rateCard, health] = await Promise.all([
    adminRequest('/admin/customers'),
    adminRequest('/admin/audit'),
    adminRequest('/admin/topups'),
    adminRequest('/admin/reservations'),
    adminRequest('/admin/openai/resolver-candidates'),
    adminRequest('/admin/openai/rate-card'),
    fetchHealth(),
  ]);
  state.customers = customers.customers;
  state.audit = audit.events;
  state.topups = topups.topups;
  state.reservations = reservations.reservations;
  state.resolverCandidates = resolverCandidates.candidates;
  state.rateCard = normalizeRateCard(rateCard);
  state.health = health;

  const nextCustomerId = preferredCustomerId ?? state.selected?.id ?? state.customers[0]?.id ?? null;
  state.selected = nextCustomerId ? state.customers.find((row) => row.id === nextCustomerId) ?? null : null;
  if (state.selected) {
    await selectCustomer(state.selected.id);
  } else {
    state.selectedAuthTokens = [];
    state.selectedApiKeys = [];
  }
  state.error = '';
  draw();
}

function isBusy(key: string): boolean {
  return state.pending.includes(key);
}

async function withPending<T>(key: string, work: () => Promise<T>): Promise<T> {
  state.pending = [...state.pending, key];
  state.error = '';
  draw();
  try {
    return await work();
  } catch (error) {
    state.error = error instanceof Error ? error.message : String(error);
    throw error;
  } finally {
    state.pending = state.pending.filter((entry) => entry !== key);
    draw();
  }
}

function setNotice(variant: ToastVariant, message: string): void {
  state.notice = { variant, message };
  draw();
}

function pendingSummary(): string {
  if (state.pending.includes('saveRateCard')) return 'Saving rate card changes...';
  if (state.pending.includes('loadRateCard')) return 'Refreshing the current rate card snapshot...';
  if (state.pending.includes('refresh')) return 'Refreshing admin data...';
  if (state.pending.includes('createCustomer')) return 'Creating customer account...';
  if (state.pending.includes('adjustBalance')) return 'Applying balance adjustment...';
  if (state.pending.includes('refundTopup')) return 'Submitting refund request...';
  if (state.pending.includes('issueCustomerApiKey')) return 'Issuing new API key...';
  if (state.pending.includes('issueCustomerAuthToken')) return 'Issuing new UI auth token...';
  if (state.pending.includes('login')) return 'Signing in...';
  return 'Working...';
}

function pageSubheading(): string {
  switch (currentTopRoute()) {
    case 'customers':
      return 'Manage customer accounts, balances, credentials, and request ledgers.';
    case 'nodes':
      return 'Inspect resolver candidates, broker routes, and price metadata.';
    case 'reservations':
      return 'Review the shared reservation ledger across all customers.';
    case 'topups':
      return 'Monitor Stripe-funded balance events and refund activity.';
    case 'rate-card':
      return 'Edit OpenAI retail pricing with structured forms and save as a single snapshot.';
    case 'audit':
      return 'Track the latest operator actions against the gateway control plane.';
    case 'health':
    default:
      return 'Operational overview for the OpenAI gateway admin control plane.';
  }
}

async function fetchHealth(): Promise<HealthSnapshot> {
  const res = await fetch('/healthz');
  return {
    status: res.ok ? (await res.text()).trim() || 'ok' : 'down',
    checkedAt: new Date().toISOString(),
  };
}

function credentialWidgetFromEvent(
  event: Event,
): { showPlaintext?: (plaintext: string) => void } | null {
  const currentTarget = event.currentTarget as { showPlaintext?: (plaintext: string) => void } | null;
  if (currentTarget) return currentTarget;
  const target = event.target as { showPlaintext?: (plaintext: string) => void } | null;
  if (target) return target;
  const [origin] = event.composedPath();
  return origin && typeof origin === 'object'
    ? (origin as { showPlaintext?: (plaintext: string) => void })
    : null;
}

function emptyRateCard(): RateCardSnapshot {
  return {
    chatTiers: PRICING_TIERS.map((tier) => ({ tier, inputUsdPerMillion: 0, outputUsdPerMillion: 0 })),
    chatModels: [],
    embeddings: [],
    audioSpeech: [],
    audioTranscripts: [],
    images: [],
  };
}

function normalizeRateCard(snapshot: RateCardSnapshot): RateCardSnapshot {
  return {
    chatTiers: PRICING_TIERS.map((tier) => {
      const row = snapshot.chatTiers.find((entry) => entry.tier === tier);
      return {
        tier,
        inputUsdPerMillion: row?.inputUsdPerMillion ?? 0,
        outputUsdPerMillion: row?.outputUsdPerMillion ?? 0,
      };
    }),
    chatModels: [...snapshot.chatModels].sort(compareRateCardRows),
    embeddings: [...snapshot.embeddings].sort(compareRateCardRows),
    audioSpeech: [...snapshot.audioSpeech].sort(compareRateCardRows),
    audioTranscripts: [...snapshot.audioTranscripts].sort(compareRateCardRows),
    images: [...snapshot.images].sort(compareRateCardRows),
  };
}

function compareRateCardRows(
  left: { isPattern: boolean; sortOrder: number; modelOrPattern: string },
  right: { isPattern: boolean; sortOrder: number; modelOrPattern: string },
): number {
  return Number(left.isPattern) - Number(right.isPattern) || left.sortOrder - right.sortOrder || left.modelOrPattern.localeCompare(right.modelOrPattern);
}

function pricingEntriesTable<T>(input: {
  heading: string;
  description: string;
  entries: T[];
  columns: string[];
  renderRow: (entry: T, index: number) => unknown;
  addRow: () => void;
}) {
  return html`
    <portal-data-table .heading=${input.heading} .description=${input.description}>
      <div slot="toolbar">
        <portal-button variant="ghost" @click=${input.addRow}>Add row</portal-button>
      </div>
      <table>
        <thead>
          <tr>${input.columns.map((label) => html`<th>${label}</th>`)}</tr>
        </thead>
        <tbody>
          ${input.entries.map((entry, index) => input.renderRow(entry, index))}
        </tbody>
      </table>
    </portal-data-table>
  `;
}

function setChatTierPrice(
  tier: PricingTier,
  field: 'inputUsdPerMillion' | 'outputUsdPerMillion',
  value: number,
): void {
  state.rateCard.chatTiers = state.rateCard.chatTiers.map((entry) =>
    entry.tier === tier ? { ...entry, [field]: value } : entry,
  );
  draw();
}

function updateChatModel(
  index: number,
  field: keyof ChatModelEntry,
  value: string | number | boolean,
): void {
  updateRow('chatModels', index, field, value);
}

function updateEmbeddings(
  index: number,
  field: keyof EmbeddingsEntry,
  value: string | number | boolean,
): void {
  updateRow('embeddings', index, field, value);
}

function updateAudioSpeech(
  index: number,
  field: keyof AudioSpeechEntry,
  value: string | number | boolean,
): void {
  updateRow('audioSpeech', index, field, value);
}

function updateAudioTranscripts(
  index: number,
  field: keyof AudioTranscriptEntry,
  value: string | number | boolean,
): void {
  updateRow('audioTranscripts', index, field, value);
}

function updateImages(
  index: number,
  field: keyof ImagesEntry,
  value: string | number | boolean,
): void {
  updateRow('images', index, field, value);
}

function updateRow<K extends 'chatModels' | 'embeddings' | 'audioSpeech' | 'audioTranscripts' | 'images'>(
  key: K,
  index: number,
  field: keyof RateCardSnapshot[K][number],
  value: string | number | boolean,
): void {
  state.rateCard[key] = state.rateCard[key].map((entry, rowIndex) =>
    rowIndex === index ? { ...entry, [field]: value } : entry,
  ) as RateCardSnapshot[K];
  draw();
}

function addRateCardRow(
  key: 'chatModels' | 'embeddings' | 'audioSpeech' | 'audioTranscripts' | 'images',
): void {
  switch (key) {
    case 'chatModels':
      state.rateCard.chatModels = [
        ...state.rateCard.chatModels,
        { modelOrPattern: '', isPattern: false, tier: 'starter', sortOrder: 100 },
      ];
      break;
    case 'embeddings':
      state.rateCard.embeddings = [
        ...state.rateCard.embeddings,
        { modelOrPattern: '', isPattern: false, usdPerMillionTokens: 0, sortOrder: 100 },
      ];
      break;
    case 'audioSpeech':
      state.rateCard.audioSpeech = [
        ...state.rateCard.audioSpeech,
        { modelOrPattern: '', isPattern: false, usdPerMillionChars: 0, sortOrder: 100 },
      ];
      break;
    case 'audioTranscripts':
      state.rateCard.audioTranscripts = [
        ...state.rateCard.audioTranscripts,
        { modelOrPattern: '', isPattern: false, usdPerMinute: 0, sortOrder: 100 },
      ];
      break;
    case 'images':
      state.rateCard.images = [
        ...state.rateCard.images,
        { modelOrPattern: '', isPattern: false, size: '1024x1024', quality: 'standard', usdPerImage: 0, sortOrder: 100 },
      ];
      break;
  }
  draw();
}

function removeRateCardRow(
  key: 'chatModels' | 'embeddings' | 'audioSpeech' | 'audioTranscripts' | 'images',
  index: number,
): void {
  switch (key) {
    case 'chatModels':
      state.rateCard.chatModels = state.rateCard.chatModels.filter((_, rowIndex) => rowIndex !== index);
      break;
    case 'embeddings':
      state.rateCard.embeddings = state.rateCard.embeddings.filter((_, rowIndex) => rowIndex !== index);
      break;
    case 'audioSpeech':
      state.rateCard.audioSpeech = state.rateCard.audioSpeech.filter((_, rowIndex) => rowIndex !== index);
      break;
    case 'audioTranscripts':
      state.rateCard.audioTranscripts = state.rateCard.audioTranscripts.filter((_, rowIndex) => rowIndex !== index);
      break;
    case 'images':
      state.rateCard.images = state.rateCard.images.filter((_, rowIndex) => rowIndex !== index);
      break;
  }
  draw();
}

function textCell(value: string, onChange: (value: string) => void) {
  return html`<input class="openai-admin-table-input" .value=${value} @input=${(event: Event) => onChange((event.currentTarget as HTMLInputElement).value)} />`;
}

function numberCell(value: number, onChange: (value: number) => void) {
  return html`<input class="openai-admin-table-input" type="number" step="0.000001" .value=${String(value)} @input=${(event: Event) => onChange(Number((event.currentTarget as HTMLInputElement).value || 0))} />`;
}

function boolCell(value: boolean, onChange: (value: boolean) => void) {
  return selectCell(value ? 'pattern' : 'exact', ['exact', 'pattern'], (next) => onChange(next === 'pattern'));
}

function tierCell(value: PricingTier, onChange: (value: PricingTier) => void) {
  return selectCell(value, PRICING_TIERS, (next) => onChange(next as PricingTier));
}

function selectCell(value: string, options: readonly string[], onChange: (value: string) => void) {
  return html`
    <select class="openai-admin-table-input" .value=${value} @change=${(event: Event) => onChange((event.currentTarget as HTMLSelectElement).value)}>
      ${options.map((option) => html`<option value=${option}>${option}</option>`)}
    </select>
  `;
}

function deleteCell(onDelete: () => void) {
  return html`<portal-button variant="ghost" @click=${onDelete}>Delete</portal-button>`;
}

function logout(): void {
  state.authToken = '';
  state.actor = '';
  state.customers = [];
  state.selected = null;
  state.selectedAuthTokens = [];
  state.selectedApiKeys = [];
  state.createdAuthToken = '';
  state.audit = [];
  state.topups = [];
  state.reservations = [];
  state.selectedReservation = null;
  sessionStorage.removeItem('openai-gateway:admin-token');
  sessionStorage.removeItem('openai-gateway:admin-actor');
  setRoute('health');
  draw();
}

function metaList(entries: readonly [string, unknown][], className = 'openai-admin-meta-list') {
  return html`
    <dl class=${className}>
      ${entries.map(
        ([label, value]) => html`
          <div class="openai-admin-meta-item">
            <dt>${label}</dt>
            <dd>${value}</dd>
          </div>
        `,
      )}
    </dl>
  `;
}

function installStyles(): void {
  if (document.getElementById('openai-gateway-admin-styles')) {
    return;
  }
  const link = document.createElement('link');
  link.id = 'openai-gateway-admin-styles';
  link.rel = 'stylesheet';
  link.href = new URL('./admin.css', import.meta.url).href;
  document.head.append(link);
}

async function adminRequest(path: string, init: RequestInit = {}): Promise<any> {
  const headers = new Headers(init.headers ?? {});
  headers.set('authorization', `Bearer ${state.authToken}`);
  headers.set('x-actor', state.actor);
  if (init.body && !headers.has('content-type')) {
    headers.set('content-type', 'application/json');
  }
  const res = await fetch(path, { ...init, headers });
  if (!res.ok) throw new Error(await res.text());
  if (res.status === 204) return null;
  return res.json();
}

function serializeCredentials(rows: CredentialSummary[]) {
  return rows.map((row) => ({
    id: row.id,
    label: row.label,
    createdAt: row.created_at,
    lastUsedAt: row.last_used_at,
    revokedAt: row.revoked_at,
  }));
}

function usd(cents: string): string {
  return `$${(Number(cents) / 100).toFixed(2)}`;
}

function formatReservationValue(cents: string | null, tokens: string | null): string {
  if (cents !== null) return usd(cents);
  if (tokens !== null) return `${tokens} tokens`;
  return 'n/a';
}
