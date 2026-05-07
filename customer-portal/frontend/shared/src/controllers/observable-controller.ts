import type { ReactiveController, ReactiveControllerHost } from 'lit';
import type { Observable, Subscription } from 'rxjs';

export class ObservableController<T> implements ReactiveController {
  private subscription: Subscription | null = null;
  value: T | undefined;

  constructor(
    private readonly host: ReactiveControllerHost,
    private readonly source: Observable<T>,
    initial?: T,
  ) {
    if (initial !== undefined) {
      this.value = initial;
    }
    host.addController(this);
  }

  hostConnected(): void {
    this.subscription = this.source.subscribe((v) => {
      this.value = v;
      this.host.requestUpdate();
    });
  }

  hostDisconnected(): void {
    this.subscription?.unsubscribe();
    this.subscription = null;
  }
}
