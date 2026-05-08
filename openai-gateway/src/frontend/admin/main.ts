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

const rootEl = document.getElementById('app');
if (!rootEl) throw new Error('missing #app');

const state = {
  auth: sessionStorage.getItem('openai-gateway:admin-auth') ?? '',
  customers: [] as Customer[],
  selected: null as Customer | null,
  audit: [] as AuditEvent[],
  query: '',
  error: '',
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
    <div style="display:grid;gap:1rem;grid-template-columns:minmax(320px,1.1fr) minmax(320px,1fr);">
      <portal-card heading="Customers" subheading="Search and inspect gateway accounts.">
        <form @submit=${searchCustomers} style="display:flex;gap:0.75rem;align-items:end;flex-wrap:wrap;">
          <portal-input name="q" label="Search" .value=${state.query}></portal-input>
          <portal-button type="submit">Search</portal-button>
          <portal-button variant="ghost" @click=${logout}>Sign out</portal-button>
        </form>
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
                <div><strong>Status:</strong> ${state.selected.status}</div>
                <div><strong>Balance:</strong> ${usd(state.selected.balance_usd_cents)}</div>
              </div>
            `
          : html`<div style="color:var(--text-2);">Select a customer to inspect details.</div>`}
      </portal-card>
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

async function selectCustomer(id: string): Promise<void> {
  const out = await adminRequest(`/admin/customers/${encodeURIComponent(id)}`);
  state.selected = out.customer;
  draw();
}

async function refresh(): Promise<void> {
  const [customers, audit] = await Promise.all([
    adminRequest('/admin/customers'),
    adminRequest('/admin/audit'),
  ]);
  state.customers = customers.customers;
  state.selected = state.customers[0] ?? null;
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
  sessionStorage.removeItem('openai-gateway:admin-auth');
  draw();
}

async function adminRequest(path: string): Promise<any> {
  const res = await fetch(path, {
    headers: { authorization: state.auth },
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

function usd(cents: string): string {
  return `$${(Number(cents) / 100).toFixed(2)}`;
}
