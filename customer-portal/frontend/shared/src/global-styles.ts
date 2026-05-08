const GLOBAL_STYLE_ID = "livepeer-network-ui-global-styles";

const GLOBAL_STYLES = `
:root {
  color-scheme: dark;

  --surface-canvas: #111111;
  --surface-0: #181818;
  --surface-1: #242424;
  --surface-2: #2a2a2a;
  --surface-3: #333333;
  --surface-overlay: rgba(24, 24, 24, 0.88);

  --text-1: #f5f7f7;
  --text-2: rgba(245, 247, 247, 0.78);
  --text-3: rgba(245, 247, 247, 0.56);

  --accent: #18794e;
  --accent-strong: #1f8c5a;
  --accent-hover: #239664;
  --accent-press: #14613f;
  --accent-tint: rgba(24, 121, 78, 0.18);
  --accent-line: rgba(24, 121, 78, 0.42);

  --success: #28a36a;
  --success-tint: rgba(40, 163, 106, 0.16);
  --warning: #d9a441;
  --warning-tint: rgba(217, 164, 65, 0.16);
  --danger: #d35c5c;
  --danger-hover: #e16d6d;
  --danger-tint: rgba(211, 92, 92, 0.16);

  --border-1: rgba(255, 255, 255, 0.08);
  --border-2: rgba(255, 255, 255, 0.14);

  --space-1: 0.25rem;
  --space-2: 0.5rem;
  --space-3: 0.75rem;
  --space-4: 1rem;
  --space-5: 1.5rem;
  --space-6: 2rem;
  --space-7: 2.5rem;
  --space-8: 3rem;
  --space-10: 4rem;
  --space-12: 6rem;

  --radius-sm: 0.375rem;
  --radius-md: 0.75rem;
  --radius-lg: 1rem;
  --radius-xl: 1.25rem;
  --radius-pill: 9999px;

  --font-sans: "ABC Favorit", "Sohne", "Inter", "Segoe UI", sans-serif;
  --font-mono: "ABC Favorit Mono", "SFMono-Regular", Consolas, monospace;
  --font-size-xs: 0.75rem;
  --font-size-sm: 0.875rem;
  --font-size-base: 1rem;
  --font-size-lg: clamp(1.125rem, 0.95rem + 0.5vw, 1.25rem);
  --font-size-xl: clamp(1.35rem, 1.05rem + 0.9vw, 1.7rem);
  --font-size-2xl: clamp(1.7rem, 1.2rem + 1.5vw, 2.4rem);
  --font-size-3xl: clamp(2.2rem, 1.45rem + 2.6vw, 3.4rem);

  --shadow-sm: 0 12px 30px rgba(0, 0, 0, 0.16);
  --shadow-md: 0 18px 48px rgba(0, 0, 0, 0.24);
  --shadow-lg: 0 24px 72px rgba(0, 0, 0, 0.32);
  --duration-fast: 120ms;
  --duration-base: 180ms;
  --duration-slow: 280ms;
  --ease-standard: cubic-bezier(0.2, 0, 0, 1);
}

*,
*::before,
*::after {
  box-sizing: border-box;
}

html {
  -webkit-text-size-adjust: 100%;
  text-size-adjust: 100%;
  scrollbar-gutter: stable;
  font-family: var(--font-sans);
  font-size: 16px;
  line-height: 1.5;
  color: var(--text-1);
  background:
    radial-gradient(circle at top, rgba(24, 121, 78, 0.14), transparent 28rem),
    linear-gradient(180deg, #0f1111 0%, var(--surface-canvas) 100%);
  accent-color: var(--accent);
}

body {
  margin: 0;
  min-height: 100dvh;
  color: var(--text-1);
  background: transparent;
}

body[data-livepeer-ui-mode="network-console"] {
  background:
    radial-gradient(circle at top left, rgba(24, 121, 78, 0.18), transparent 24rem),
    linear-gradient(180deg, #101111 0%, #171818 100%);
}

body[data-livepeer-ui-mode="product-app"] {
  background:
    radial-gradient(circle at top right, rgba(24, 121, 78, 0.12), transparent 28rem),
    linear-gradient(180deg, #111213 0%, #181818 100%);
}

h1, h2, h3, h4, h5, h6, p, ul, ol, dl, figure, blockquote {
  margin: 0;
}

ul, ol {
  padding: 0;
  list-style: none;
}

img, picture, svg, video, canvas {
  display: block;
  max-width: 100%;
}

button, input, select, textarea {
  font: inherit;
  color: inherit;
}

button {
  background: none;
  border: 0;
  padding: 0;
  cursor: pointer;
}

a {
  color: inherit;
}

h1, h2, h3 {
  line-height: 1.1;
  letter-spacing: -0.02em;
  color: var(--text-1);
}

p {
  color: var(--text-2);
}

code, pre, kbd, samp {
  font-family: var(--font-mono);
}

input, textarea, select {
  background: rgba(255, 255, 255, 0.03);
  color: var(--text-1);
  border: 1px solid var(--border-1);
  border-radius: var(--radius-md);
  padding: var(--space-2) var(--space-3);
  transition:
    border-color var(--duration-fast) var(--ease-standard),
    box-shadow var(--duration-fast) var(--ease-standard),
    background var(--duration-fast) var(--ease-standard);
}

input:focus-visible,
textarea:focus-visible,
select:focus-visible {
  outline: 0;
  border-color: var(--accent-line);
  box-shadow: 0 0 0 3px var(--accent-tint);
  background: rgba(255, 255, 255, 0.045);
}

::selection {
  background: var(--accent-tint);
  color: var(--text-1);
}
`;

export function installGlobalStyles(): void {
  if (typeof document === "undefined") {
    return;
  }
  if (document.getElementById(GLOBAL_STYLE_ID) !== null) {
    return;
  }
  const style = document.createElement("style");
  style.id = GLOBAL_STYLE_ID;
  style.textContent = GLOBAL_STYLES;
  document.head.append(style);
}
