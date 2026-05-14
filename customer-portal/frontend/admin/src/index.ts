import '@livepeer-network-modules/customer-portal-shared';
import { HashRouter } from '@livepeer-network-modules/customer-portal-shared';

export function bootstrap(root: HTMLElement): void {
  document.body.dataset.livepeerUiMode = 'network-console';
  const router = new HashRouter();
  router
    .add('/customers', () => {
      root.innerHTML =
        '<portal-card heading="Customers"><div>Customer list scaffold</div></portal-card>';
    })
    .add('/customers/:id', (_path, params) => {
      root.innerHTML = `<portal-card heading="Customer ${params['id']}"><div>Detail scaffold</div></portal-card>`;
    })
    .add('/topups', () => {
      root.innerHTML = '<portal-card heading="Top-ups"><div>Top-up scaffold</div></portal-card>';
    })
    .add('/audit', () => {
      root.innerHTML = '<portal-card heading="Audit"><div>Audit feed scaffold</div></portal-card>';
    });
  router.start();
}
