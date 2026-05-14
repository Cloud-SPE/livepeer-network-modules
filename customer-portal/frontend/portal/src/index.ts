import '@livepeer-network-modules/customer-portal-shared';
import { HashRouter } from '@livepeer-network-modules/customer-portal-shared';

export function bootstrap(root: HTMLElement): void {
  document.body.dataset.livepeerUiMode = 'product-app';
  const router = new HashRouter();
  router
    .add('/signup', () => {
      root.innerHTML = '<portal-card heading="Create account"><portal-signup></portal-signup></portal-card>';
    })
    .add('/login', () => {
      root.innerHTML = '<portal-card heading="Sign in"><portal-login></portal-login></portal-card>';
    })
    .add('/account', () => {
      root.innerHTML =
        '<portal-card heading="Account"><portal-balance balanceCents="0" reservedCents="0"></portal-balance></portal-card>';
    })
    .add('/api-keys', () => {
      root.innerHTML = '<portal-card heading="API keys"><portal-api-keys></portal-api-keys></portal-card>';
    })
    .add('/billing', () => {
      root.innerHTML =
        '<portal-card heading="Billing"><portal-checkout-button amountCents="1000">Top up $10</portal-checkout-button></portal-card>';
    });
  router.start();
}
