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

const rootEl = document.getElementById('app');
if (!rootEl) throw new Error('missing #app');

const state = {
  auth: sessionStorage.getItem('openai-gateway:admin-auth') ?? '',
  customers: [] as Customer[],
  selected: null as Customer | null,
  audit: [] as AuditEvent[],
  topups: [] as Topup[],
  query: '',
  error: '',
  createEmail: '',
  createTier: 'free',
  adjustDelta: '',
  adjustReason: '',
  refundSessionId: '',
  refundReason: '',
};

void bootstrap();

async function bootstrap(): Promise<void> {
  if (state.auth) {
    await refresh().catch(() => undefined);
  }
  draw();
}

function draw(): void {
  render(
    html`
      <portal-layout brand="OpenAI Gateway Admin">
        ${state.auth ? dashboardView() : loginView()}
        <span slot="footer">Operator console</span>
      </portal-layout>
    `,
    rootEl!,
  );
}

function loginView() {
  return html`
    <portal-card heading="Admin login" tone="accent">
      <form @submit=${login}>
        <portal-input name="user" label="User" required></portal-input>
        <portal-input name="pass" label="Password" type="password" required></portal-input>
        <div style="margin-top:1rem">
          <portal-button type="submit">Sign in</portal-button>
        </div>
      </form>
    </portal-card>
    ${state.error ? html`<div style="margin-top:1rem"><portal-toast variant="danger" .message=${state.error}></portal-toast></div>` : ''}
  `;
}

function dashboardView() {
  return html`
    <div style="display:grid;gap:1rem;grid-template-columns:minmax(340px,1.05fr) minmax(340px,1fr);">
      <portal-card heading="Customers" subheading="Search and inspect gateway accounts.">
        <form @submit=${searchCustomers} style="display:flex;gap:0.75rem;align-items:end;flex-wrap:wrap;">
          <portal-input name="q" label="Search" .value=${state.query}></portal-input>
          <portal-button type="submit">Search</portal-button>
          <portal-button variant="ghost" @click=${logout}>Sign out</portal-button>
        </form>
        <portal-detail-section heading="Create customer" description="Provision a new portal customer and initial API account.">
          <form @submit=${createCustomer} style="display:grid;gap:0.75rem;">
            <portal-input
              name="email"
              label="Customer email"
              .value=${state.createEmail}
              @portal-input-change=${(e: CustomEvent<{ value: string }>) => (state.createEmail = e.detail.value)}
            ></portal-input>
            <label style="display:grid;gap:0.5rem;color:var(--text-2);font-size:var(--font-size-sm);">
              Tier
              <select
                name="tier"
                .value=${state.createTier}
                @change=${(e: Event) => (state.createTier = (e.currentTarget as HTMLSelectElement).value)}
                style="border-radius:12px;border:1px solid var(--border-1);background:rgba(255,255,255,0.03);color:var(--text-1);padding:0.9rem;font:inherit;"
              >
                <option value="free">free</option>
                <option value="prepaid">prepaid</option>
              </select>
            </label>
            <portal-action-row>
              <portal-button type="submit">Create customer</portal-button>
            </portal-action-row>
          </form>
        </portal-detail-section>
        <div style="margin-top:1rem;display:grid;gap:0.5rem;">
          ${state.customers.map(
            (row) => html`
              <portal-detail-section heading=${row.email} description=${row.id}>
                <div style="display:flex;justify-content:space-between;gap:1rem;align-items:center;">
                  <div>
                    <portal-status-pill label=${row.status}></portal-status-pill>
                    <span style="margin-left:0.75rem;color:var(--text-2);">${usd(row.balance_usd_cents)}</span>
                  </div>
                  <portal-button variant="ghost" @click=${() => selectCustomer(row.id)}>Open</portal-button>
                </div>
              </portal-detail-section>
            `,
          )}
        </div>
      </portal-card>
      <portal-card heading="Customer detail" subheading="Balance and status for the selected account.">
        ${state.selected
          ? html`
              <div style="display:grid;gap:0.75rem;">
                <div><strong>ID:</strong> ${state.selected.id}</div>
                <div><strong>Email:</strong> ${state.selected.email}</div>
                <div><strong>Tier:</strong> ${state.selected.tier}</div>
                <div><strong>Status:</strong> <portal-status-pill label=${state.selected.status}></portal-status-pill></div>
                <div><strong>Balance:</strong> ${usd(state.selected.balance_usd_cents)}</div>
                <div><strong>Reserved:</strong> ${usd(state.selected.reserved_usd_cents)}</div>
              </div>
              <portal-detail-section heading="Balance adjustment" description="Apply a manual debit or credit.">
                <form @submit=${adjustBalance} style="display:grid;gap:0.75rem;">
                  <portal-input
                    name="delta"
                    label="Delta USD cents"
                    placeholder="500 or -500"
                    .value=${state.adjustDelta}
                    @portal-input-change=${(e: CustomEvent<{ value: string }>) => (state.adjustDelta = e.detail.value)}
                  ></portal-input>
                  <portal-input
                    name="reason"
                    label="Reason"
                    .value=${state.adjustReason}
                    @portal-input-change=${(e: CustomEvent<{ value: string }>) => (state.adjustReason = e.detail.value)}
                  ></portal-input>
                  <portal-action-row>
                    <portal-button type="submit">Apply balance change</portal-button>
                  </portal-action-row>
                </form>
              </portal-detail-section>
              <portal-detail-section heading="Account status" description="Suspend, reactivate, or close the customer.">
                <portal-action-row>
                  <portal-button variant="ghost" @click=${() => setStatus('active')}>Set active</portal-button>
                  <portal-button variant="ghost" @click=${() => setStatus('suspended')}>Suspend</portal-button>
                  <portal-button variant="danger" @click=${() => setStatus('closed')}>Close</portal-button>
                </portal-action-row>
              </portal-detail-section>
              <portal-detail-section heading="Refund top-up" description="Reverse a previously credited Stripe checkout session.">
                <form @submit=${refundTopup} style="display:grid;gap:0.75rem;">
                  <portal-input
                    name="stripe_session_id"
                    label="Stripe session ID"
                    .value=${state.refundSessionId}
                    @portal-input-change=${(e: CustomEvent<{ value: string }>) => (state.refundSessionId = e.detail.value)}
                  ></portal-input>
                  <portal-input
                    name="reason"
                    label="Reason"
                    .value=${state.refundReason}
                    @portal-input-change=${(e: CustomEvent<{ value: string }>) => (state.refundReason = e.detail.value)}
                  ></portal-input>
                  <portal-action-row>
                    <portal-button variant="danger" type="submit">Refund top-up</portal-button>
                  </portal-action-row>
                </form>
              </portal-detail-section>
            `
          : html`<div style="color:var(--text-2);">Select a customer to inspect details.</div>`}
      </portal-card>
    </div>
    <div style="margin-top:1rem">
      <portal-data-table heading="Recent top-ups" description="Latest credited and refunded balance events.">
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
    </div>
    <div style="margin-top:1rem">
      <portal-data-table heading="Recent admin audit" description="Latest operator actions against the gateway account system.">
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
    </div>
    ${state.error ? html`<div style="margin-top:1rem"><portal-toast variant="danger" .message=${state.error}></portal-toast></div>` : ''}
  `;
}

async function login(event: Event): Promise<void> {
  event.preventDefault();
  const form = new FormData(event.currentTarget as HTMLFormElement);
  state.auth = `Basic ${btoa(`${String(form.get('user') ?? '')}:${String(form.get('pass') ?? '')}`)}`;
  sessionStorage.setItem('openai-gateway:admin-auth', state.auth);
  await refresh();
}

async function searchCustomers(event: Event): Promise<void> {
  event.preventDefault();
  const form = new FormData(event.currentTarget as HTMLFormElement);
  state.query = String(form.get('q') ?? '');
  const out = await adminRequest(`/admin/customers${state.query ? `?q=${encodeURIComponent(state.query)}` : ''}`);
  state.customers = out.customers;
  draw();
}

async function createCustomer(event: Event): Promise<void> {
  event.preventDefault();
  const out = await adminRequest('/admin/customers', {
    method: 'POST',
    body: JSON.stringify({
      email: state.createEmail,
      tier: state.createTier,
    }),
  });
  state.createEmail = '';
  await refresh(out.customer.id);
}

async function selectCustomer(id: string): Promise<void> {
  const out = await adminRequest(`/admin/customers/${encodeURIComponent(id)}`);
  state.selected = out.customer;
  draw();
}

async function adjustBalance(event: Event): Promise<void> {
  event.preventDefault();
  if (!state.selected) return;
  await adminRequest(`/admin/customers/${encodeURIComponent(state.selected.id)}/balance`, {
    method: 'POST',
    body: JSON.stringify({
      delta_usd_cents: state.adjustDelta,
      reason: state.adjustReason,
    }),
  });
  state.adjustDelta = '';
  state.adjustReason = '';
  await refresh(state.selected.id);
}

async function setStatus(status: 'active' | 'suspended' | 'closed'): Promise<void> {
  if (!state.selected) return;
  await adminRequest(`/admin/customers/${encodeURIComponent(state.selected.id)}/status`, {
    method: 'POST',
    body: JSON.stringify({ status }),
  });
  await refresh(state.selected.id);
}

async function refundTopup(event: Event): Promise<void> {
  event.preventDefault();
  if (!state.selected) return;
  await adminRequest(`/admin/customers/${encodeURIComponent(state.selected.id)}/refund`, {
    method: 'POST',
    body: JSON.stringify({
      stripe_session_id: state.refundSessionId,
      reason: state.refundReason,
    }),
  });
  state.refundSessionId = '';
  state.refundReason = '';
  await refresh(state.selected.id);
}

async function refresh(preferredCustomerId?: string): Promise<void> {
  const [customers, audit, topups] = await Promise.all([
    adminRequest('/admin/customers'),
    adminRequest('/admin/audit'),
    adminRequest('/admin/topups'),
  ]);
  state.customers = customers.customers;
  state.topups = topups.topups;
  state.selected =
    (preferredCustomerId
      ? state.customers.find((row: Customer) => row.id === preferredCustomerId)
      : null) ??
    state.selected ??
    state.customers[0] ??
    null;
  state.audit = audit.events;
  if (state.selected) {
    const detail = await adminRequest(`/admin/customers/${encodeURIComponent(state.selected.id)}`);
    state.selected = detail.customer;
  }
  state.error = '';
  draw();
}

function logout(): void {
  state.auth = '';
  state.customers = [];
  state.selected = null;
  state.audit = [];
  state.topups = [];
  sessionStorage.removeItem('openai-gateway:admin-auth');
  draw();
}

async function adminRequest(path: string, init: RequestInit = {}): Promise<any> {
  const headers = new Headers(init.headers ?? {});
  headers.set('authorization', state.auth);
  if (init.body && !headers.has('content-type')) headers.set('content-type', 'application/json');
  const res = await fetch(path, {
    ...init,
    headers,
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

function usd(cents: string): string {
  return `$${(Number(cents) / 100).toFixed(2)}`;
}
