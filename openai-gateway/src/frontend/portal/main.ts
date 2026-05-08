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

type ApiKeySummary = {
  id: string;
  label: string | null;
  created_at: string;
  last_used_at: string | null;
  revoked_at: string | null;
};

const rootEl = document.getElementById('app');
if (!rootEl) throw new Error('missing #app');

const state = {
  apiKey: localStorage.getItem('openai-gateway:portal-api-key') ?? '',
  customer: null as Customer | null,
  keys: [] as ApiKeySummary[],
  topups: [] as Array<{
    id: string;
    stripe_session_id: string;
    amount_usd_cents: string;
    status: string;
    created_at: string;
  }>,
  tab: 'playground' as 'playground' | 'account' | 'keys' | 'billing',
  error: '',
  response: '',
  loading: false,
};

void bootstrap();

async function bootstrap(): Promise<void> {
  if (state.apiKey) {
    await loginWithKey(state.apiKey).catch(() => undefined);
  }
  draw();
}

function draw(): void {
  render(
    html`
      <portal-layout brand="Livepeer OpenAI Gateway">
        <div slot="nav" style="margin-top:0.75rem;display:flex;gap:0.5rem;flex-wrap:wrap;">
          ${navButton('playground', 'Playground')}
          ${navButton('account', 'Account')}
          ${navButton('keys', 'API Keys')}
          ${navButton('billing', 'Billing')}
          ${state.customer
            ? html`<portal-button variant="ghost" @click=${logout}>Sign out</portal-button>`
            : ''}
        </div>
        ${state.customer ? dashboardView() : authView()}
        <span slot="footer">OpenAI-compatible customer portal</span>
      </portal-layout>
    `,
    rootEl!,
  );
}

function authView() {
  return html`
    <div style="display:grid;gap:1rem;grid-template-columns:repeat(auto-fit,minmax(320px,1fr));">
      <portal-card heading="Create portal access" tone="accent">
        <form @submit=${signup}>
          <portal-input name="email" label="Email" type="email" required></portal-input>
          <div style="margin-top:1rem">
            <portal-button type="submit">Create account</portal-button>
          </div>
        </form>
      </portal-card>
      <portal-card heading="Use an existing API key">
        <form @submit=${login}>
          <portal-input name="api_key" label="API key" required></portal-input>
          <div style="margin-top:1rem">
            <portal-button type="submit">Enter portal</portal-button>
          </div>
        </form>
      </portal-card>
    </div>
    ${state.error ? html`<div style="margin-top:1rem"><portal-toast variant="danger" .message=${state.error}></portal-toast></div>` : ''}
  `;
}

function dashboardView() {
  return html`
    <div style="display:grid;gap:1rem;">
      <div style="display:grid;gap:1rem;grid-template-columns:repeat(auto-fit,minmax(220px,1fr));">
        <portal-metric-tile label="Customer" value=${state.customer!.email}></portal-metric-tile>
        <portal-metric-tile label="Tier" value=${state.customer!.tier}></portal-metric-tile>
        <portal-metric-tile label="Status" value=${state.customer!.status}></portal-metric-tile>
        <portal-metric-tile label="Balance" value=${usd(state.customer!.balance_usd_cents)}></portal-metric-tile>
      </div>
      ${state.tab === 'playground' ? playgroundView() : ''}
      ${state.tab === 'account' ? accountView() : ''}
      ${state.tab === 'keys' ? keysView() : ''}
      ${state.tab === 'billing' ? billingView() : ''}
      ${state.error ? html`<portal-toast variant="danger" .message=${state.error}></portal-toast>` : ''}
    </div>
  `;
}

function playgroundView() {
  return html`
    <portal-card heading="Playground" subheading="Try the first OpenAI surface set directly against this gateway.">
      <form @submit=${runPlayground}>
        <label style="display:grid;gap:0.5rem;color:var(--text-2);font-size:var(--font-size-sm);">
          API
          <select
            name="capability"
            style="border-radius:12px;border:1px solid var(--border-1);background:rgba(255,255,255,0.03);color:var(--text-1);padding:0.9rem;font:inherit;"
          >
            <option value="chat">Chat completions</option>
            <option value="embeddings">Embeddings</option>
            <option value="images">Image generation</option>
            <option value="speech">Audio speech</option>
            <option value="transcriptions">Audio transcription</option>
          </select>
        </label>
        <portal-input name="model" label="Model / offering" value="gpt-4o-mini"></portal-input>
        <div style="margin-top:1rem">
          <label style="display:grid;gap:0.5rem;color:var(--text-2);font-size:var(--font-size-sm);">
            Prompt / input
            <textarea
              name="input_text"
              rows="8"
              style="border-radius:12px;border:1px solid var(--border-1);background:rgba(255,255,255,0.03);color:var(--text-1);padding:0.9rem;font:inherit;"
            >Say hello in one sentence.</textarea>
          </label>
        </div>
        <div style="margin-top:1rem">
          <label style="display:grid;gap:0.5rem;color:var(--text-2);font-size:var(--font-size-sm);">
            Audio file (used only for transcription)
            <input
              name="audio_file"
              type="file"
              accept="audio/*"
              style="border-radius:12px;border:1px solid var(--border-1);background:rgba(255,255,255,0.03);color:var(--text-1);padding:0.9rem;font:inherit;"
            />
          </label>
        </div>
        <div style="margin-top:1rem">
          <portal-button type="submit" ?disabled=${state.loading}>${state.loading ? 'Running…' : 'Send request'}</portal-button>
        </div>
      </form>
      <div style="margin-top:1rem">
        <pre style="white-space:pre-wrap;word-break:break-word;border:1px solid var(--border-1);border-radius:12px;padding:1rem;background:rgba(255,255,255,0.02);min-height:10rem;">${state.response || 'No response yet.'}</pre>
      </div>
    </portal-card>
  `;
}

function accountView() {
  return html`
    <portal-detail-section heading="Account overview" description="Current customer record from the gateway.">
      <div style="display:grid;gap:0.75rem;">
        <div><strong>ID:</strong> ${state.customer!.id}</div>
        <div><strong>Email:</strong> ${state.customer!.email}</div>
        <div><strong>Balance:</strong> ${usd(state.customer!.balance_usd_cents)}</div>
        <div><strong>Reserved:</strong> ${usd(state.customer!.reserved_usd_cents)}</div>
      </div>
    </portal-detail-section>
  `;
}

function keysView() {
  return html`
    <portal-card heading="API Keys" subheading="Issue and revoke customer credentials.">
      <portal-api-keys
        .keys=${state.keys.map((row) => ({
          id: row.id,
          label: row.label,
          createdAt: row.created_at,
          lastUsedAt: row.last_used_at,
          revokedAt: row.revoked_at,
        }))}
        @portal-api-key-issue=${issueKey}
        @portal-api-key-revoke=${revokeKey}
      ></portal-api-keys>
    </portal-card>
  `;
}

function billingView() {
  return html`
    <portal-card heading="Billing and credits" subheading="Fund the prepaid balance used by your gateway API keys.">
      <div style="display:flex;gap:0.75rem;flex-wrap:wrap;align-items:center;">
        <portal-checkout-button
          action="/portal/topups/checkout"
          .amountCents=${1000}
          .authToken=${state.apiKey}
          @portal-checkout-error=${(e: CustomEvent<{ message: string }>) => {
            state.error = e.detail.message;
            draw();
          }}
        >Top up $10</portal-checkout-button>
        <portal-checkout-button action="/portal/topups/checkout" .amountCents=${2500} .authToken=${state.apiKey}>Top up $25</portal-checkout-button>
        <portal-checkout-button action="/portal/topups/checkout" .amountCents=${5000} .authToken=${state.apiKey}>Top up $50</portal-checkout-button>
      </div>
      <div style="margin-top:1rem">
        <portal-data-table heading="Top-up history" description="Latest Stripe-funded balance movements for this customer.">
          <table>
            <thead>
              <tr><th>Created</th><th>Amount</th><th>Status</th><th>Session</th></tr>
            </thead>
            <tbody>
              ${state.topups.map(
                (row) => html`<tr>
                  <td>${row.created_at}</td>
                  <td>${usd(row.amount_usd_cents)}</td>
                  <td><portal-status-pill label=${row.status}></portal-status-pill></td>
                  <td>${row.stripe_session_id}</td>
                </tr>`,
              )}
            </tbody>
          </table>
        </portal-data-table>
      </div>
    </portal-card>
  `;
}

function navButton(tab: typeof state.tab, label: string) {
  return html`<portal-button variant=${state.tab === tab ? 'primary' : 'ghost'} @click=${() => switchTab(tab)}>${label}</portal-button>`;
}

function switchTab(tab: typeof state.tab): void {
  state.tab = tab;
  state.error = '';
  draw();
}

async function signup(event: Event): Promise<void> {
  event.preventDefault();
  const form = new FormData(event.currentTarget as HTMLFormElement);
  const email = String(form.get('email') ?? '');
  const out = await post('/portal/signup', { email });
  state.apiKey = out.api_key;
  localStorage.setItem('openai-gateway:portal-api-key', state.apiKey);
  await loginWithKey(state.apiKey);
}

async function login(event: Event): Promise<void> {
  event.preventDefault();
  const form = new FormData(event.currentTarget as HTMLFormElement);
  const apiKey = String(form.get('api_key') ?? '');
  await loginWithKey(apiKey);
}

async function loginWithKey(apiKey: string): Promise<void> {
  const out = await post('/portal/login', { api_key: apiKey });
  state.apiKey = apiKey;
  localStorage.setItem('openai-gateway:portal-api-key', apiKey);
  state.customer = out.customer;
  state.keys = out.api_keys;
  state.topups = (await authRequest('/portal/topups')).topups;
  state.error = '';
  draw();
}

async function issueKey(event: CustomEvent<{ label: string }>): Promise<void> {
  const out = await authRequest('/portal/api-keys', {
    method: 'POST',
    body: JSON.stringify({ label: event.detail.label || undefined }),
  });
  const widget = event.currentTarget as { showPlaintext?: (plaintext: string) => void };
  widget.showPlaintext?.(out.api_key);
  await refresh();
}

async function revokeKey(event: CustomEvent<{ id: string }>): Promise<void> {
  await authRequest(`/portal/api-keys/${encodeURIComponent(event.detail.id)}`, { method: 'DELETE' });
  await refresh();
}

async function runPlayground(event: Event): Promise<void> {
  event.preventDefault();
  const form = new FormData(event.currentTarget as HTMLFormElement);
  state.loading = true;
  state.error = '';
  draw();
  try {
    const capability = String(form.get('capability') ?? 'chat');
    const model = String(form.get('model') ?? 'gpt-4o-mini');
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
        out = await authRequest('/v1/audio/transcriptions', {
          method: 'POST',
          body: multipart,
        });
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
  } catch (err) {
    state.response = '';
    state.error = errorMessage(err);
  } finally {
    state.loading = false;
    draw();
  }
}

async function refresh(): Promise<void> {
  state.customer = (await authRequest('/portal/account')).customer;
  state.keys = (await authRequest('/portal/api-keys')).api_keys;
  state.topups = (await authRequest('/portal/topups')).topups;
  draw();
}

function logout(): void {
  state.apiKey = '';
  state.customer = null;
  state.keys = [];
  state.topups = [];
  state.response = '';
  localStorage.removeItem('openai-gateway:portal-api-key');
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
  headers.set('authorization', `Bearer ${state.apiKey}`);
  if (init.body && !(init.body instanceof FormData) && !headers.has('content-type')) {
    headers.set('content-type', 'application/json');
  }
  const res = await fetch(path, { ...init, headers });
  if (!res.ok) throw new Error(await res.text());
  if (res.status === 204) return null;
  return res.json();
}

function usd(cents: string): string {
  return `$${(Number(cents) / 100).toFixed(2)}`;
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}
