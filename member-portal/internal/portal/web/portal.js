'use strict';
const notice = document.querySelector('#notice');
function inform(message) { notice.textContent = message; notice.scrollIntoView({block: 'nearest'}); }
async function api(path, body, method = 'POST') {
  const response = await fetch(path, {method, credentials: 'same-origin', headers: {'Content-Type': 'application/json'}, body: body === undefined ? undefined : JSON.stringify(body)});
  const text = await response.text();
  if (!response.ok) throw new Error(text || `Request failed (${response.status})`);
  return text ? JSON.parse(text) : null;
}
document.querySelector('#signin')?.addEventListener('click', async event => {
  event.currentTarget.disabled = true;
  try {
    if (!window.ethereum?.request) throw new Error('Open this page with an Ethereum wallet browser extension.');
    const [wallet] = await window.ethereum.request({method: 'eth_requestAccounts'});
    const challenge = await api('/api/auth/challenge', {wallet});
    const message = '0x' + Array.from(new TextEncoder().encode(challenge.message), byte => byte.toString(16).padStart(2, '0')).join('');
    const signature = await window.ethereum.request({method: 'personal_sign', params: [message, wallet]});
    await api('/api/auth/login', {id: challenge.id, signature});
    window.location.reload();
  } catch (error) { inform(error.message); event.currentTarget.disabled = false; }
});
document.querySelector('#logout')?.addEventListener('click', async () => {
  try { await api('/api/auth/logout', {}); window.location.reload(); } catch (error) { inform(error.message); }
});
for (const form of document.querySelectorAll('.member-action')) form.addEventListener('submit', async event => {
  event.preventDefault();
  const button = form.querySelector('button'); button.disabled = true;
  try {
    const values = Object.fromEntries(new FormData(form));
    if (form.dataset.action === 'enroll') values.gpu_uuids = values.gpu_uuids.split(/[\s,]+/).filter(Boolean);
    const result = await api(form.getAttribute('action'), values, form.dataset.method || 'POST');
    if (result?.bootstrap_command) {
      document.querySelector('#install-command').value = result.bootstrap_command;
      document.querySelector('#install-expiry').textContent = `Pool ${result.pool_id}. Enrollment ${result.enrollment_id}. Link expires ${result.expires_at}.`;
      const link = new URL(result.bundle_url, window.location.origin);
      if (link.origin !== window.location.origin || !/^\/bootstrap\/[a-f0-9]{64}\/bundle$/.test(link.pathname)) throw new Error('Invalid install link.');
      document.querySelector('#download-bundle').href = link.href;
      document.querySelector('#install-result').showModal();
    } else { window.location.reload(); }
  } catch (error) { inform(error.message); } finally { button.disabled = false; }
});
document.querySelector('#copy-command')?.addEventListener('click', async () => {
  try { await navigator.clipboard.writeText(document.querySelector('#install-command').value); inform('Install command copied.'); }
  catch { inform('Select and copy the command manually.'); }
});
document.querySelector('#install-result')?.addEventListener('close', () => window.location.reload());
