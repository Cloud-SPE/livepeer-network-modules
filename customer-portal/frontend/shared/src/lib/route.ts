export type RouteHandler = (path: string, params: Record<string, string>) => void;

export interface RouteEntry {
  pattern: string;
  handler: RouteHandler;
}

export class HashRouter {
  private routes: RouteEntry[] = [];

  add(pattern: string, handler: RouteHandler): this {
    this.routes.push({ pattern, handler });
    return this;
  }

  start(): void {
    window.addEventListener('hashchange', () => this.dispatch());
    this.dispatch();
  }

  navigate(path: string): void {
    window.location.hash = path;
  }

  private dispatch(): void {
    const path = (window.location.hash || '#/').slice(1);
    for (const route of this.routes) {
      const params = match(route.pattern, path);
      if (params) {
        route.handler(path, params);
        return;
      }
    }
  }
}

function match(pattern: string, path: string): Record<string, string> | null {
  const patternParts = pattern.split('/').filter(Boolean);
  const pathParts = path.split('/').filter(Boolean);
  if (patternParts.length !== pathParts.length) return null;
  const params: Record<string, string> = {};
  for (let i = 0; i < patternParts.length; i++) {
    const p = patternParts[i]!;
    const v = pathParts[i]!;
    if (p.startsWith(':')) {
      params[p.slice(1)] = decodeURIComponent(v);
    } else if (p !== v) {
      return null;
    }
  }
  return params;
}
