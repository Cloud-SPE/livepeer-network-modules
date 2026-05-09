import '@livepeer-rewrite/customer-portal-shared';
import { clearSession, readSession, writeSession } from '@livepeer-rewrite/customer-portal-shared';
import { html, render } from 'lit';

type Customer = {
  id: string;
  email: string;
  tier: string;
  status: string;
  balance_usd_cents: string;
  reserved_usd_cents: string;
};

type CredentialSummary = {
  id: string;
  label: string | null;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
};

type Limits = {
  quota_tokens_remaining: string | null;
  quota_monthly_allowance: string | null;
  quota_reserved_tokens: string;
  quota_reset_at: string | null;
  balance_usd_cents: string;
  reserved_usd_cents: string;
};

type Topup = {
  id: string;
  stripe_session_id: string;
  amount_usd_cents: string;
  status: string;
  created_at: string;
};

type Reservation = {
  id: string;
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

type GroupedUsage = {
  day: string;
  model: string;
  capability: string;
  reservations: number;
  amount_usd_cents: string;
  committed_usd_cents: string;
  amount_tokens: string;
  committed_tokens: string;
};

type PlaygroundModel = {
  id: string;
  capability: string;
  offering: string;
  broker_url: string;
  eth_address: string;
  price_per_work_unit_wei: string;
  work_unit: string;
  brokerUrl: string;
  ethAddress: string;
  pricePerWorkUnitWei: string;
  workUnit: string;
  extra: unknown;
  constraints: unknown;
};

type PortalRoute = 'dashboard' | 'playground' | 'keys' | 'usage' | 'billing' | 'settings';

const ROUTES: readonly PortalRoute[] = ['dashboard', 'playground', 'keys', 'usage', 'billing', 'settings'];

const rootEl = document.getElementById('app');
if (!rootEl) throw new Error('missing #app');

installStyles();

const state = {
  authToken: readSession()?.token ?? '',
  actor: readSession()?.actor ?? '',
  customer: null as Customer | null,
  keys: [] as CredentialSummary[],
  authTokens: [] as CredentialSummary[],
  limits: null as Limits | null,
  topups: [] as Topup[],
  reservations: [] as Reservation[],
  groupedUsage: [] as GroupedUsage[],
  selectedReservation: null as Reservation | null,
  route: resolveRoute(),
  checkoutStatus: resolveCheckoutStatus(),
  customTopupDollars: '25',
  error: '',
  notice: null as null | { variant: 'success' | 'warning'; message: string },
  pending: [] as string[],
  response: '',
  playgroundModels: [] as PlaygroundModel[],
  playgroundCapability: 'chat' as PlaygroundApi,
  playgroundModel: '',
  playgroundInput: 'Say hello in one sentence.',
  loading: false,
};

type PlaygroundApi = 'chat' | 'embeddings' | 'images' | 'speech' | 'transcriptions';

window.addEventListener('hashchange', () => {
  state.route = resolveRoute();
  state.checkoutStatus = resolveCheckoutStatus();
  draw();
});

void bootstrap();

async function bootstrap(): Promise<void> {
  if (!location.hash) {
    setRoute('dashboard');
  }
  if (state.authToken && state.actor) {
    await loginWithToken(state.authToken, state.actor).catch(() => undefined);
  }
  draw();
}

function draw(): void {
  render(
    html`
      <portal-layout brand="Livepeer OpenAI Gateway">
        ${state.customer ? navView() : ''}
        ${state.customer ? shellView() : authView()}
        <span slot="footer">OpenAI-compatible customer portal</span>
      </portal-layout>
    `,
    rootEl!,
  );
}

function navView() {
  return html`
    <nav slot="nav" class="openai-portal-nav" aria-label="Primary">
      ${ROUTES.map((route) => navButton(route, labelForRoute(route)))}
      <portal-button variant="ghost" ?loading=${isBusy('logout')} @click=${logout}>Sign out</portal-button>
    </nav>
  `;
}

function authView() {
  return html`
    <div class="openai-portal-auth-grid">
      <portal-card heading="Create portal access" tone="accent">
        <form @submit=${signup} class="openai-portal-form">
          <portal-input name="email" label="Email" type="email" required></portal-input>
          <div class="openai-portal-section-gap">
            <portal-button type="submit" ?loading=${isBusy('signup')}>Create account</portal-button>
          </div>
        </form>
      </portal-card>
      <portal-card heading="Use an existing auth token">
        <form @submit=${login} class="openai-portal-form">
          <portal-input name="token" label="Auth token" required></portal-input>
          <portal-input name="actor" label="Actor" required></portal-input>
          <div class="openai-portal-section-gap">
            <portal-button type="submit" ?loading=${isBusy('login')}>Enter portal</portal-button>
          </div>
        </form>
      </portal-card>
    </div>
    ${feedbackView()}
  `;
}

function shellView() {
  return html`
    <div class="openai-portal-shell">
      <portal-card heading=${labelForRoute(state.route)} subheading=${pageSubheading()}>
        <div class="openai-portal-session-grid">
          <div class="openai-portal-session-meta">
            <div class="openai-portal-eyebrow">Session</div>
            <div class="openai-portal-session-value">${state.customer?.email ?? state.actor}</div>
            <div class="openai-portal-session-copy">
              ${state.customer ? `${state.customer.tier} tier · ${state.customer.status}` : 'Authenticate to continue'}
            </div>
          </div>
          <div class="openai-portal-session-meta">
            <div class="openai-portal-eyebrow">Activity</div>
            <div class="openai-portal-session-value">${pendingSummary()}</div>
            <div class="openai-portal-session-copy">
              The portal stays responsive while background refreshes and writes complete.
            </div>
          </div>
        </div>
      </portal-card>
      ${feedbackView()}
      ${routeView()}
    </div>
  `;
}

function routeView() {
  switch (state.route) {
    case 'playground':
      return playgroundView();
    case 'keys':
      return keysView();
    case 'usage':
      return usageView();
    case 'billing':
      return billingView();
    case 'settings':
      return settingsView();
    case 'dashboard':
    default:
      return dashboardView();
  }
}

function dashboardView() {
  return html`
    <div class="openai-portal-stack">
      <div class="openai-portal-metrics">
        <portal-metric-tile label="Customer" value=${state.customer!.email}></portal-metric-tile>
        <portal-metric-tile label="Tier" value=${state.customer!.tier}></portal-metric-tile>
        <portal-metric-tile label="Status" value=${state.customer!.status}></portal-metric-tile>
        <portal-metric-tile label="Balance" value=${usd(state.customer!.balance_usd_cents)}></portal-metric-tile>
      </div>
      <div class="openai-portal-detail-grid">
        <portal-detail-section heading="Account overview" description="Current customer record from the gateway.">
          ${metaList([
            ['ID', state.customer!.id],
            ['Email', state.customer!.email],
            ['Reserved', usd(state.customer!.reserved_usd_cents)],
            ['UI auth tokens', String(countActive(state.authTokens))],
            ['Product API keys', String(countActive(state.keys))],
          ])}
        </portal-detail-section>
        <portal-detail-section heading="Usage snapshot" description="Quick monthly and request rollups from settled reservations.">
          ${metaList([
            ['Requests tracked', String(state.reservations.length)],
            ['Usage buckets', String(state.groupedUsage.length)],
            ['Quota remaining', state.limits?.quota_tokens_remaining ?? '—'],
            ['Quota reset', state.limits?.quota_reset_at ?? '—'],
          ])}
        </portal-detail-section>
      </div>
      <portal-card heading="Recent activity" subheading="Latest reservations and top-ups across this customer account.">
        <div class="openai-portal-card-grid">
          <portal-data-table heading="Recent reservations" description="Most recent gateway requests.">
            <table data-mobile-card="true">
              <thead>
                <tr><th>Created</th><th>Capability</th><th>Status</th><th>Committed</th></tr>
              </thead>
              <tbody>
                ${state.reservations.slice(0, 5).map(
                  (row) => html`<tr>
                    <td data-label="Created">${row.created_at}</td>
                    <td data-label="Capability">${row.capability ?? row.kind}</td>
                    <td data-label="Status"><portal-status-pill label=${row.state}></portal-status-pill></td>
                    <td data-label="Committed">${formatReservationValue(row.committed_usd_cents, row.committed_tokens)}</td>
                  </tr>`,
                )}
              </tbody>
            </table>
          </portal-data-table>
          <portal-data-table
            heading="Recent top-ups"
            description="Latest Stripe-funded balance movements."
            ?empty=${state.topups.length === 0}
            empty-heading="No top-ups yet"
            empty-message="Top-up history will appear here after your first successful Stripe checkout."
          >
            <table data-mobile-card="true">
              <thead>
                <tr><th>Created</th><th>Amount</th><th>Status</th></tr>
              </thead>
              <tbody>
                ${state.topups.slice(0, 5).map(
                  (row) => html`<tr>
                    <td data-label="Created">${row.created_at}</td>
                    <td data-label="Amount">${usd(row.amount_usd_cents)}</td>
                    <td data-label="Status"><portal-status-pill label=${row.status}></portal-status-pill></td>
                  </tr>`,
                )}
              </tbody>
            </table>
          </portal-data-table>
        </div>
      </portal-card>
    </div>
  `;
}

function playgroundView() {
  const options = modelsForApi(state.playgroundCapability);
  const selected = selectedPlaygroundModel();
  return html`
    <portal-card heading="Playground" subheading="Try the first OpenAI surface set directly against this gateway.">
      <form @submit=${runPlayground} class="openai-portal-form">
        <label class="openai-portal-field">
          API
          <select
            class="openai-portal-field-control"
            name="capability"
            .value=${state.playgroundCapability}
            @change=${(event: Event) => updatePlaygroundCapability((event.currentTarget as HTMLSelectElement).value as PlaygroundApi)}
          >
            <option value="chat">Chat completions</option>
            <option value="embeddings">Embeddings</option>
            <option value="images">Image generation</option>
            <option value="speech">Audio speech</option>
            <option value="transcriptions">Audio transcription</option>
          </select>
        </label>
        <label class="openai-portal-field">
          Model
          <select
            class="openai-portal-field-control"
            name="model"
            .value=${state.playgroundModel}
            @change=${(event: Event) => {
              state.playgroundModel = (event.currentTarget as HTMLSelectElement).value;
              draw();
            }}
          >
            ${options.length === 0
              ? html`<option value="">No discovered models</option>`
              : options.map(
                  (model) => html`<option value=${model.id}>${model.id} · ${model.offering}</option>`,
                )}
          </select>
        </label>
        <div class="openai-portal-section-gap">
          <label class="openai-portal-field">
            Prompt / input
            <textarea
              class="openai-portal-field-control"
              name="input_text"
              rows="8"
              @input=${(event: Event) => {
                state.playgroundInput = (event.currentTarget as HTMLTextAreaElement).value;
              }}
            >${state.playgroundInput}</textarea>
          </label>
        </div>
        <div class="openai-portal-section-gap">
          <label class="openai-portal-field">
            Audio file (used only for transcription)
            <input class="openai-portal-field-control" name="audio_file" type="file" accept="audio/*" />
          </label>
        </div>
        <div class="openai-portal-section-gap">
          <portal-button type="submit" ?loading=${state.loading}>Send request</portal-button>
        </div>
      </form>
      <div class="openai-portal-section-gap openai-portal-playground-grid">
        <portal-detail-section
          heading="Discovered model"
          description="Resolver-backed route metadata surfaced from /v1/models for customer selection."
        >
          ${selected
            ? html`
                ${metaList(playgroundModelMeta(selected), 'openai-portal-meta-list openai-portal-meta-list--tight')}
                ${playgroundMetadataSections(selected)}
              `
            : html`<p class="openai-portal-empty">No discovered model is available for this API yet.</p>`}
        </portal-detail-section>
        <portal-detail-section
          heading="Available models"
          description="Current discovered choices for the selected API surface."
        >
          ${options.length === 0
            ? html`<p class="openai-portal-empty">No resolver-backed models discovered yet.</p>`
            : html`
                <ul class="openai-portal-model-list">
                  ${options.map(
                    (model) => html`
                      <li class=${model.id === state.playgroundModel ? 'current' : ''}>
                        <button type="button" @click=${() => {
                          state.playgroundModel = model.id;
                          draw();
                        }}>${model.id}</button>
                        <span>${model.offering}</span>
                      </li>
                    `,
                  )}
                </ul>
              `}
        </portal-detail-section>
      </div>
      <div class="openai-portal-section-gap">
        <pre class="openai-portal-response">${state.response || 'No response yet.'}</pre>
      </div>
    </portal-card>
  `;
}

function keysView() {
  return html`
    <div class="openai-portal-key-grid">
      <portal-card heading="UI auth tokens" subheading="Portal login credentials for this customer.">
        <portal-api-keys
          .keys=${serializeCredentials(state.authTokens)}
          @portal-api-key-issue=${issueAuthToken}
          @portal-api-key-revoke=${revokeAuthToken}
        ></portal-api-keys>
      </portal-card>
      <portal-card heading="API keys" subheading="Product credentials for calling the gateway APIs.">
        <portal-api-keys
          .keys=${serializeCredentials(state.keys)}
          @portal-api-key-issue=${issueKey}
          @portal-api-key-revoke=${revokeKey}
        ></portal-api-keys>
      </portal-card>
    </div>
  `;
}

function usageView() {
  return html`
    <portal-card heading="Usage and request history" subheading="Grouped analytics plus raw reservation/request drilldown.">
      <portal-data-table heading="Grouped analytics" description="Usage rolled up by day, model, and capability.">
        <table data-mobile-card="true">
          <thead>
            <tr><th>Day</th><th>Model</th><th>Capability</th><th>Requests</th><th>Reserved</th><th>Committed</th></tr>
          </thead>
          <tbody>
            ${state.groupedUsage.map(
              (row) => html`<tr>
                <td data-label="Day">${row.day}</td>
                <td data-label="Model">${row.model}</td>
                <td data-label="Capability">${row.capability}</td>
                <td data-label="Requests">${row.reservations}</td>
                <td data-label="Reserved">${formatReservationValue(row.amount_usd_cents, row.amount_tokens)}</td>
                <td data-label="Committed">${formatReservationValue(row.committed_usd_cents, row.committed_tokens)}</td>
              </tr>`,
            )}
          </tbody>
        </table>
      </portal-data-table>
      <div class="openai-portal-section-gap">
        <portal-data-table
          heading="Reservation ledger"
          description="Tracks reserved, committed, and refunded usage records."
          ?empty=${state.reservations.length === 0}
          empty-heading="No reservations yet"
          empty-message="Requests and settled usage records will appear here after you start calling the gateway."
        >
          <table data-mobile-card="true">
            <thead>
              <tr>
                <th>Created</th>
                <th>Capability</th>
                <th>Model</th>
                <th>Status</th>
                <th>Reserved</th>
                <th>Committed</th>
                <th>Refunded</th>
                <th>Work ID</th>
                <th></th>
              </tr>
            </thead>
            <tbody>
              ${state.reservations.map(
                (row) => html`<tr>
                  <td data-label="Created">${row.created_at}</td>
                  <td data-label="Capability">${row.capability ?? row.kind}</td>
                  <td data-label="Model">${row.model ?? 'n/a'}</td>
                  <td data-label="Status"><portal-status-pill label=${row.state}></portal-status-pill></td>
                  <td data-label="Reserved">${formatReservationValue(row.amount_usd_cents, row.amount_tokens)}</td>
                  <td data-label="Committed">${formatReservationValue(row.committed_usd_cents, row.committed_tokens)}</td>
                  <td data-label="Refunded">${formatReservationValue(row.refunded_usd_cents, row.refunded_tokens)}</td>
                  <td data-label="Work ID">${row.work_id}</td>
                  <td data-label="Actions"><portal-button variant="ghost" @click=${() => openReservation(row.id)}>Open</portal-button></td>
                </tr>`,
              )}
            </tbody>
          </table>
        </portal-data-table>
      </div>
      ${state.selectedReservation
        ? html`
            <div class="openai-portal-section-gap">
              <portal-detail-section heading="Request detail" description="Selected reservation and settled usage details.">
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
                  'openai-portal-meta-list openai-portal-meta-list--tight',
                )}
              </portal-detail-section>
            </div>
          `
        : ''}
    </portal-card>
  `;
}

function billingView() {
  return html`
    <portal-card heading="Billing and credits" subheading="Prepaid balance, custom top-ups, Stripe return flow, and settlement history.">
      <div class="openai-portal-action-row">
        <portal-button ?loading=${isBusy('checkout')} @click=${() => startCheckout(1000)}>Top up $10</portal-button>
        <portal-button ?loading=${isBusy('checkout')} @click=${() => startCheckout(2500)}>Top up $25</portal-button>
        <portal-button ?loading=${isBusy('checkout')} @click=${() => startCheckout(5000)}>Top up $50</portal-button>
      </div>
      <div class="openai-portal-section-gap openai-portal-section-gap--constrained">
        <form @submit=${submitCustomTopup} class="openai-portal-form">
          <portal-input
            name="custom_amount"
            label="Custom amount (USD)"
            .value=${state.customTopupDollars}
            @portal-input-change=${(event: CustomEvent<{ value: string }>) => {
              state.customTopupDollars = event.detail.value;
            }}
          ></portal-input>
          <div class="openai-portal-section-gap">
            <portal-button type="submit" ?loading=${isBusy('checkout')}>Start custom checkout</portal-button>
          </div>
        </form>
      </div>
      <div class="openai-portal-section-gap">
        <portal-data-table
          heading="Top-up history"
          description="Latest Stripe-funded balance movements for this customer."
          ?empty=${state.topups.length === 0}
          empty-heading="No balance movements yet"
          empty-message="Your Stripe-funded top-up history will appear here after a successful checkout."
        >
          <table data-mobile-card="true">
            <thead>
              <tr><th>Created</th><th>Amount</th><th>Status</th><th>Session</th></tr>
            </thead>
            <tbody>
              ${state.topups.map(
                (row) => html`<tr>
                  <td data-label="Created">${row.created_at}</td>
                  <td data-label="Amount">${usd(row.amount_usd_cents)}</td>
                  <td data-label="Status"><portal-status-pill label=${row.status}></portal-status-pill></td>
                  <td data-label="Session">${row.stripe_session_id}</td>
                </tr>`,
              )}
            </tbody>
          </table>
        </portal-data-table>
      </div>
    </portal-card>
  `;
}

function settingsView() {
  return html`
    <div class="openai-portal-settings-grid">
      <portal-detail-section heading="Account overview" description="Read-only customer metadata.">
        ${metaList([
          ['ID', state.customer!.id],
          ['Email', state.customer!.email],
          ['Tier', state.customer!.tier],
          ['Status', state.customer!.status],
          ['Balance', usd(state.customer!.balance_usd_cents)],
          ['Reserved', usd(state.customer!.reserved_usd_cents)],
        ])}
      </portal-detail-section>
      <portal-detail-section heading="Limits" description="Read-only account and quota limits backed by the gateway account API.">
        ${metaList([
          ['Quota remaining', state.limits?.quota_tokens_remaining ?? '—'],
          ['Monthly allowance', state.limits?.quota_monthly_allowance ?? '—'],
          ['Quota reserved', state.limits?.quota_reserved_tokens ?? '—'],
          ['Quota reset', state.limits?.quota_reset_at ?? '—'],
        ])}
      </portal-detail-section>
    </div>
  `;
}

function navButton(route: PortalRoute, label: string) {
  return html`<portal-button variant=${state.route === route ? 'primary' : 'ghost'} @click=${() => setRoute(route)}>${label}</portal-button>`;
}

function feedbackView() {
  return html`
    ${state.notice ? html`<portal-toast variant=${state.notice.variant} .message=${state.notice.message}></portal-toast>` : ''}
    ${state.checkoutStatus === 'success'
      ? html`<portal-toast variant="success" message="Stripe checkout completed. Balance history will refresh as the webhook settles."></portal-toast>`
      : ''}
    ${state.checkoutStatus === 'cancel'
      ? html`<portal-toast variant="warning" message="Top-up checkout was cancelled before completion."></portal-toast>`
      : ''}
    ${state.error ? html`<portal-toast variant="danger" .message=${state.error}></portal-toast>` : ''}
  `;
}

function resolveRoute(): PortalRoute {
  const hash = location.hash.replace(/^#/, '');
  const [rawRoute] = hash.split('?');
  return ROUTES.includes(rawRoute as PortalRoute) ? (rawRoute as PortalRoute) : 'dashboard';
}

function resolveCheckoutStatus(): 'success' | 'cancel' | '' {
  const hash = location.hash.replace(/^#/, '');
  const [, query = ''] = hash.split('?');
  const params = new URLSearchParams(query);
  const checkout = params.get('checkout');
  return checkout === 'success' || checkout === 'cancel' ? checkout : '';
}

function setRoute(route: PortalRoute, params?: Record<string, string>): void {
  const search = params ? new URLSearchParams(params).toString() : '';
  location.hash = search ? `${route}?${search}` : route;
}

function labelForRoute(route: PortalRoute): string {
  switch (route) {
    case 'playground':
      return 'Playground';
    case 'keys':
      return 'Keys';
    case 'usage':
      return 'Usage';
    case 'billing':
      return 'Billing';
    case 'settings':
      return 'Settings';
    case 'dashboard':
    default:
      return 'Dashboard';
  }
}

function pageSubheading(): string {
  switch (state.route) {
    case 'playground':
      return 'Exercise the OpenAI-compatible APIs with immediate request and response feedback.';
    case 'keys':
      return 'Issue and revoke login credentials and product API keys with plaintext reveal on creation.';
    case 'usage':
      return 'Review grouped usage analytics, reservation history, and per-request settlement details.';
    case 'billing':
      return 'Manage prepaid balance through Stripe checkout and inspect top-up history.';
    case 'settings':
      return 'Read-only account metadata, tier status, and quota limits.';
    case 'dashboard':
    default:
      return 'Account health, recent usage, and billing posture in one operator-friendly view.';
  }
}

function setNotice(variant: 'success' | 'warning', message: string): void {
  state.notice = { variant, message };
}

function isBusy(key: string): boolean {
  return state.pending.includes(key);
}

function pendingSummary(): string {
  return state.pending.length ? `Working on ${state.pending.length} task${state.pending.length === 1 ? '' : 's'}.` : 'Ready';
}

async function signup(event: Event): Promise<void> {
  event.preventDefault();
  const form = new FormData(event.currentTarget as HTMLFormElement);
  const email = String(form.get('email') ?? '');
  const out = await withPending('signup', () => post('/portal/signup', { email }));
  setNotice('success', 'Portal access created. Loading customer workspace.');
  await loginWithToken(out.auth_token, 'customer');
}

async function login(event: Event): Promise<void> {
  event.preventDefault();
  const form = new FormData(event.currentTarget as HTMLFormElement);
  const token = String(form.get('token') ?? '');
  const actor = String(form.get('actor') ?? '');
  await withPending('login', () => loginWithToken(token, actor));
}

async function loginWithToken(token: string, actor: string): Promise<void> {
  const out = await post('/portal/login', { token, actor });
  state.authToken = token;
  state.actor = actor;
  writeSession({
    token,
    actor,
    ...(out.customer?.id ? { customerId: out.customer.id } : {}),
    ...(out.customer?.email ? { email: out.customer.email } : {}),
  });
  setNotice('success', 'Portal session refreshed.');
  await refresh();
}

async function refresh(): Promise<void> {
  state.customer = (await withPending('refresh', () => authRequest('/portal/account'))).customer;
  state.authTokens = (await authRequest('/portal/auth-tokens')).auth_tokens;
  state.keys = (await authRequest('/portal/api-keys')).api_keys;
  state.topups = (await authRequest('/portal/topups')).topups;
  state.limits = (await authRequest('/portal/account/limits')).limits;
  const usage = await authRequest('/portal/usage');
  const models = await authRequest('/v1/models');
  state.playgroundModels = normalizePlaygroundModels(models.data ?? []);
  ensurePlaygroundSelection();
  state.reservations = usage.reservations;
  state.groupedUsage = usage.grouped;
  state.error = '';
  draw();
}

async function issueKey(event: CustomEvent<{ label: string }>): Promise<void> {
  const widget = credentialWidgetFromEvent(event);
  const out = await withPending('issue-api-key', () =>
    authRequest('/portal/api-keys', {
      method: 'POST',
      body: JSON.stringify({ label: event.detail.label || undefined }),
    }),
  );
  widget.showPlaintext?.(out.api_key);
  setNotice('success', 'API key issued.');
  await refresh();
}

async function revokeKey(event: CustomEvent<{ id: string }>): Promise<void> {
  await withPending('revoke-api-key', () =>
    authRequest(`/portal/api-keys/${encodeURIComponent(event.detail.id)}`, { method: 'DELETE' }),
  );
  setNotice('success', 'API key revoked.');
  await refresh();
}

async function issueAuthToken(event: CustomEvent<{ label: string }>): Promise<void> {
  const widget = credentialWidgetFromEvent(event);
  const out = await withPending('issue-auth-token', () =>
    authRequest('/portal/auth-tokens', {
      method: 'POST',
      body: JSON.stringify({ label: event.detail.label || undefined }),
    }),
  );
  widget.showPlaintext?.(out.auth_token);
  setNotice('success', 'Auth token issued.');
  await refresh();
}

async function revokeAuthToken(event: CustomEvent<{ id: string }>): Promise<void> {
  await withPending('revoke-auth-token', () =>
    authRequest(`/portal/auth-tokens/${encodeURIComponent(event.detail.id)}`, { method: 'DELETE' }),
  );
  setNotice('success', 'Auth token revoked.');
  await refresh();
}

async function openReservation(id: string): Promise<void> {
  const out = await withPending('reservation-detail', () => authRequest(`/portal/usage/${encodeURIComponent(id)}`));
  state.selectedReservation = out.reservation;
  draw();
}

async function submitCustomTopup(event: Event): Promise<void> {
  event.preventDefault();
  const dollars = Number(state.customTopupDollars);
  if (!Number.isFinite(dollars) || dollars <= 0) {
    state.error = 'enter a positive custom amount';
    draw();
    return;
  }
  await startCheckout(Math.round(dollars * 100));
}

async function startCheckout(amountCents: number): Promise<void> {
  const out = await withPending('checkout', () =>
    authRequest('/portal/topups/checkout', {
      method: 'POST',
      body: JSON.stringify({ amount_usd_cents: amountCents }),
    }),
  );
  if (!out?.url) {
    throw new Error('missing checkout URL');
  }
  location.href = String(out.url);
}

async function runPlayground(event: Event): Promise<void> {
  event.preventDefault();
  const form = new FormData(event.currentTarget as HTMLFormElement);
  state.loading = true;
  state.error = '';
  state.notice = null;
  draw();
  try {
    const capability = String(form.get('capability') ?? 'chat');
    const model = String(form.get('model') ?? state.playgroundModel);
    if (!model) {
      throw new Error('no discovered model is available for the selected API');
    }
    const inputText = String(form.get('input_text') ?? '');
    let out: unknown;
    switch (capability) {
      case 'embeddings':
        out = await authRequest('/v1/embeddings', {
          method: 'POST',
          body: JSON.stringify({ model, input: [inputText] }),
        });
        break;
      case 'images':
        out = await authRequest('/v1/images/generations', {
          method: 'POST',
          body: JSON.stringify({ model, prompt: inputText, size: '1024x1024', quality: 'standard' }),
        });
        break;
      case 'speech':
        out = await authRequest('/v1/audio/speech', {
          method: 'POST',
          body: JSON.stringify({ model, input: inputText, voice: 'alloy' }),
        });
        break;
      case 'transcriptions': {
        const multipart = new FormData();
        multipart.set('model', model);
        const audioFile = form.get('audio_file');
        if (audioFile instanceof File && audioFile.size > 0) {
          multipart.set('file', audioFile);
        } else {
          throw new Error('choose an audio file for transcription');
        }
        out = await authRequest('/v1/audio/transcriptions', { method: 'POST', body: multipart });
        break;
      }
      case 'chat':
      default:
        out = await authRequest('/v1/chat/completions', {
          method: 'POST',
          body: JSON.stringify({
            model,
            messages: [{ role: 'user', content: inputText }],
            stream: false,
          }),
        });
        break;
    }
    state.response = JSON.stringify(out, null, 2);
    setNotice('success', 'Playground request completed.');
  } catch (err) {
    state.response = '';
    state.error = errorMessage(err);
  } finally {
    state.loading = false;
    draw();
  }
}

async function logout(): Promise<void> {
  await withPending('logout', async () => undefined);
  state.authToken = '';
  state.actor = '';
  state.customer = null;
  state.keys = [];
  state.authTokens = [];
  state.limits = null;
  state.topups = [];
  state.reservations = [];
  state.groupedUsage = [];
  state.selectedReservation = null;
  state.response = '';
  state.notice = null;
  state.pending = [];
  clearSession();
  setRoute('dashboard');
  draw();
}

async function post(path: string, body: unknown): Promise<any> {
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

async function authRequest(path: string, init: RequestInit = {}): Promise<any> {
  const headers = new Headers(init.headers ?? {});
  headers.set('authorization', `Bearer ${state.authToken}`);
  headers.set('x-actor', state.actor);
  if (init.body && !(init.body instanceof FormData) && !headers.has('content-type')) {
    headers.set('content-type', 'application/json');
  }
  const res = await fetch(path, { ...init, headers });
  if (!res.ok) throw new Error(await res.text());
  if (res.status === 204) return null;
  return res.json();
}

function credentialWidgetFromEvent(event: Event): { showPlaintext?: (plaintext: string) => void } {
  return ((event.currentTarget ?? event.target) as { showPlaintext?: (plaintext: string) => void } | null) ?? {};
}

async function withPending<T>(key: string, work: () => Promise<T>): Promise<T> {
  state.pending = [...state.pending, key];
  draw();
  try {
    return await work();
  } finally {
    state.pending = state.pending.filter((entry) => entry !== key);
    draw();
  }
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

function countActive(rows: CredentialSummary[]): number {
  return rows.filter((row) => !row.revoked_at).length;
}

function usd(cents: string): string {
  return `$${(Number(cents) / 100).toFixed(2)}`;
}

function formatReservationValue(cents: string | null, tokens: string | null): string {
  if (cents !== null) return usd(cents);
  if (tokens !== null) return `${tokens} tokens`;
  return 'n/a';
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function normalizePlaygroundModels(rows: any[]): PlaygroundModel[] {
  return rows.map((row) => ({
    id: String(row.id ?? ''),
    capability: String(row.capability ?? ''),
    offering: String(row.offering ?? ''),
    broker_url: String(row.broker_url ?? ''),
    eth_address: String(row.eth_address ?? ''),
    price_per_work_unit_wei: String(row.price_per_work_unit_wei ?? ''),
    work_unit: String(row.work_unit ?? ''),
    brokerUrl: String(row.broker_url ?? ''),
    ethAddress: String(row.eth_address ?? ''),
    pricePerWorkUnitWei: String(row.price_per_work_unit_wei ?? ''),
    workUnit: String(row.work_unit ?? ''),
    extra: row.extra ?? null,
    constraints: row.constraints ?? null,
  }));
}

function updatePlaygroundCapability(capability: PlaygroundApi): void {
  state.playgroundCapability = capability;
  const first = modelsForApi(capability)[0];
  state.playgroundModel = first?.id ?? '';
  draw();
}

function ensurePlaygroundSelection(): void {
  const options = modelsForApi(state.playgroundCapability);
  if (options.some((model) => model.id === state.playgroundModel)) return;
  state.playgroundModel = options[0]?.id ?? '';
}

function modelsForApi(api: PlaygroundApi): PlaygroundModel[] {
  const capability = capabilityForPlaygroundApi(api);
  return state.playgroundModels.filter((model) => model.capability === capability);
}

function selectedPlaygroundModel(): PlaygroundModel | null {
  return modelsForApi(state.playgroundCapability).find((model) => model.id === state.playgroundModel) ?? null;
}

function capabilityForPlaygroundApi(api: PlaygroundApi): string {
  switch (api) {
    case 'embeddings':
      return 'openai:embeddings';
    case 'images':
      return 'openai:images-generations';
    case 'speech':
      return 'openai:audio-speech';
    case 'transcriptions':
      return 'openai:audio-transcriptions';
    case 'chat':
    default:
      return 'openai:chat-completions';
  }
}

function playgroundModelMeta(model: PlaygroundModel): [string, unknown][] {
  return [
    ['Capability', model.capability],
    ['Model', model.id],
    ['Offering', model.offering],
    ['Broker URL', model.brokerUrl || '—'],
    ['Orchestrator', model.ethAddress || '—'],
    ['Price per work unit', model.pricePerWorkUnitWei || '—'],
    ['Work unit', model.workUnit || '—'],
  ];
}

function playgroundMetadataSections(model: PlaygroundModel) {
  const extras = describeMetadata(model.extra);
  const constraints = describeMetadata(model.constraints);
  return html`
    <div class="openai-portal-playground-meta-grid">
      <div>
        <div class="openai-portal-eyebrow">Extras</div>
        ${extras.length
          ? metaList(extras, 'openai-portal-meta-list openai-portal-meta-list--tight')
          : html`<p class="openai-portal-empty">No surfaced extras.</p>`}
      </div>
      <div>
        <div class="openai-portal-eyebrow">Constraints</div>
        ${constraints.length
          ? metaList(constraints, 'openai-portal-meta-list openai-portal-meta-list--tight')
          : html`<p class="openai-portal-empty">No surfaced constraints.</p>`}
      </div>
    </div>
  `;
}

function describeMetadata(value: unknown): [string, unknown][] {
  const out: [string, unknown][] = [];
  collectMetadata('', value, out);
  return out;
}

function collectMetadata(prefix: string, value: unknown, out: [string, unknown][]): void {
  if (value === null || value === undefined) return;
  if (Array.isArray(value)) {
    if (value.length > 0) out.push([prefix || 'value', value.join(', ')]);
    return;
  }
  if (typeof value === 'object') {
    for (const [key, nested] of Object.entries(value as Record<string, unknown>)) {
      collectMetadata(prefix ? `${prefix}.${key}` : key, nested, out);
    }
    return;
  }
  out.push([prefix || 'value', String(value)]);
}

function metaList(entries: readonly [string, unknown][], className = 'openai-portal-meta-list') {
  return html`
    <dl class=${className}>
      ${entries.map(
        ([label, value]) => html`
          <div class="openai-portal-meta-item">
            <dt>${label}</dt>
            <dd>${value}</dd>
          </div>
        `,
      )}
    </dl>
  `;
}

function installStyles(): void {
  if (document.getElementById('openai-gateway-portal-styles')) {
    return;
  }
  const link = document.createElement('link');
  link.id = 'openai-gateway-portal-styles';
  link.rel = 'stylesheet';
  link.href = new URL('./portal.css', import.meta.url).href;
  document.head.append(link);
}
