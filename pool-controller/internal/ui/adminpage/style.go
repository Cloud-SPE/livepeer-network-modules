package adminpage

const pageStart = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Pool Control Plane</title>
  <style>
`

const pageStyles = `
    :root {
      --bg: #f3efe6;
      --panel: #fffaf2;
      --ink: #1f2430;
      --muted: #5d6777;
      --line: #d5c7af;
      --accent: #9b3d23;
      --accent-soft: #f2d7c6;
      --ok: #2f6f4f;
      --warn: #8c5d09;
      --bad: #9b243b;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: Georgia, "Iowan Old Style", serif;
      background:
        radial-gradient(circle at top left, rgba(155,61,35,0.14), transparent 28%),
        linear-gradient(180deg, #f9f4ea 0%, var(--bg) 100%);
      color: var(--ink);
    }
    .shell {
      max-width: 1280px;
      margin: 0 auto;
      padding: 24px;
    }
    .hero {
      display: grid;
      gap: 12px;
      margin-bottom: 22px;
      padding: 24px;
      border: 1px solid var(--line);
      background: linear-gradient(135deg, rgba(155,61,35,0.08), rgba(255,250,242,0.98));
    }
    .eyebrow {
      font-size: 12px;
      letter-spacing: 0.12em;
      text-transform: uppercase;
      color: var(--muted);
    }
    h1 {
      margin: 0;
      font-size: clamp(28px, 4vw, 48px);
      line-height: 0.95;
    }
    .lede {
      margin: 0;
      color: var(--muted);
      max-width: 70ch;
    }
    .toolbar {
      display: flex;
      flex-wrap: wrap;
      gap: 10px;
      align-items: end;
      margin-bottom: 22px;
      padding: 16px;
      border: 1px solid var(--line);
      background: var(--panel);
    }
    label {
      display: grid;
      gap: 6px;
      font-size: 13px;
      color: var(--muted);
    }
    input, textarea, select, button {
      font: inherit;
    }
    input, textarea, select {
      width: 100%;
      padding: 10px 12px;
      border: 1px solid var(--line);
      background: #fff;
      color: var(--ink);
    }
    textarea { min-height: 120px; resize: vertical; }
    button {
      border: 1px solid var(--accent);
      background: var(--accent);
      color: #fffaf2;
      padding: 10px 14px;
      cursor: pointer;
    }
    button.secondary {
      background: transparent;
      color: var(--accent);
    }
    .status {
      min-height: 22px;
      font-size: 14px;
      color: var(--muted);
      margin-bottom: 18px;
    }
    .grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
      gap: 18px;
    }
    .panel {
      border: 1px solid var(--line);
      background: var(--panel);
      padding: 16px;
      display: grid;
      gap: 12px;
    }
    .panel h2 {
      margin: 0;
      font-size: 20px;
    }
    .form-grid {
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
      gap: 10px;
    }
    .actions {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
    }
    .card-list {
      display: grid;
      gap: 10px;
    }
    .card {
      border: 1px solid var(--line);
      background: #fff;
      padding: 12px;
      display: grid;
      gap: 8px;
    }
    .card strong { font-size: 16px; }
    .row {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
      align-items: center;
    }
    .pill {
      display: inline-block;
      padding: 2px 8px;
      border: 1px solid var(--line);
      font-size: 12px;
      color: var(--muted);
    }
    .mono {
      font-family: "SFMono-Regular", Consolas, monospace;
      font-size: 12px;
      word-break: break-all;
    }
    pre {
      margin: 0;
      overflow: auto;
      padding: 12px;
      background: #fff;
      border: 1px solid var(--line);
      font-size: 12px;
    }
    .ok { color: var(--ok); }
    .warn { color: var(--warn); }
    .bad { color: var(--bad); }
    .small { font-size: 12px; color: var(--muted); }
    .check-list {
      display: grid;
      gap: 6px;
    }
    .check {
      border: 1px solid var(--line);
      background: #fff;
      padding: 8px;
      font-size: 12px;
    }
    details {
      border-top: 1px solid var(--line);
      padding-top: 10px;
    }
    summary {
      cursor: pointer;
      color: var(--muted);
      font-size: 13px;
    }
`

const pageMiddle = `
  </style>
</head>
`
