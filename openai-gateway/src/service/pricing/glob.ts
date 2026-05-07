const compiledCache = new Map<string, RegExp>();

function compileGlob(glob: string): RegExp {
  const cached = compiledCache.get(glob);
  if (cached) return cached;

  let re = '^';
  for (const ch of glob) {
    if (ch === '*') re += '.*';
    else if (ch === '?') re += '.';
    else re += ch.replace(/[.+^${}()|[\]\\]/g, '\\$&');
  }
  re += '$';

  const compiled = new RegExp(re);
  compiledCache.set(glob, compiled);
  return compiled;
}

export function globMatch(glob: string, input: string): boolean {
  return compileGlob(glob).test(input);
}

export function _resetGlobCacheForTests(): void {
  compiledCache.clear();
}
