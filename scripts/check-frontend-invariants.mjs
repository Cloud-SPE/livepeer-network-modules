import { readdir, readFile, writeFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";

const repoRoot = process.cwd();
const baselinePath = path.join(repoRoot, "scripts", "frontend-invariants-allowlist.json");

const frontendRoots = [
  "customer-portal/frontend",
  "openai-gateway/src/frontend",
  "video-gateway/src/frontend",
  "vtuber-gateway/src/frontend",
];

const skipDirs = new Set([
  "dist",
  "dist-test",
  "node_modules",
  ".turbo",
  ".vite",
  "coverage",
]);

const textExtensions = new Set([
  ".ts",
  ".js",
  ".mjs",
  ".cjs",
  ".tsx",
  ".jsx",
  ".html",
  ".css",
]);

const rules = [
  {
    id: "inline-style-attr",
    description: "forbid inline style attributes in frontend source",
    pattern: /style\s*=/g,
  },
  {
    id: "static-styles-css",
    description: "forbid CSS-in-TS static styles blocks",
    pattern: /static\s+styles\s*=\s*css/g,
  },
  {
    id: "shadow-root",
    description: "forbid shadowRoot-dependent frontend code and tests",
    pattern: /\.shadowRoot\b/g,
  },
  {
    id: "create-render-root",
    description: "forbid shadow-DOM render root hooks",
    pattern: /\bcreateRenderRoot\s*\(/g,
  },
  {
    id: "lit-element",
    description: "forbid LitElement-based UI classes in frontend packages",
    pattern: /\bLitElement\b/g,
  },
  {
    id: "nav-slot-div",
    description: "forbid generic div wrappers in nav slots; use a real nav landmark",
    pattern: /<div[^>]*slot="nav"[^>]*>/g,
  },
  {
    id: "clickable-noninteractive",
    description: "forbid clickable div/span controls; use buttons or links",
    pattern: /<(?:div|span)\b[^>]*@click=/g,
  },
  {
    id: "nonsemantic-button-role",
    description: "forbid role=button on non-button elements in frontend source",
    pattern: /<(?!button\b)[a-z0-9-]+\b[^>]*\brole="button"/g,
  },
  {
    id: "strong-label-metadata",
    description: "forbid div/strong label-value metadata rows; use dl/dt/dd",
    pattern: /<strong>\s*[^<:\n]+:\s*<\/strong>/g,
  },
];

async function walk(dir, files) {
  const entries = await readdir(dir, { withFileTypes: true });
  for (const entry of entries) {
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (skipDirs.has(entry.name)) {
        continue;
      }
      await walk(fullPath, files);
      continue;
    }
    if (!textExtensions.has(path.extname(entry.name))) {
      continue;
    }
    files.push(fullPath);
  }
}

function countMatches(content, pattern) {
  let count = 0;
  for (const _match of content.matchAll(pattern)) {
    count += 1;
  }
  return count;
}

async function collectCounts() {
  const files = [];
  for (const root of frontendRoots) {
    await walk(path.join(repoRoot, root), files);
  }

  const counts = {};
  for (const filePath of files) {
    const relPath = path.relative(repoRoot, filePath).replaceAll(path.sep, "/");
    const content = await readFile(filePath, "utf8");
    for (const rule of rules) {
      const count = countMatches(content, rule.pattern);
      if (count === 0) {
        continue;
      }
      if (!counts[rule.id]) {
        counts[rule.id] = {};
      }
      counts[rule.id][relPath] = count;
    }
  }

  return counts;
}

function sortObject(obj) {
  return Object.fromEntries(
    Object.entries(obj)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([key, value]) => {
        if (value && typeof value === "object" && !Array.isArray(value)) {
          return [key, sortObject(value)];
        }
        return [key, value];
      }),
  );
}

function compareCounts(baseline, current) {
  const failures = [];
  const improvements = [];

  for (const rule of rules) {
    const baselineFiles = baseline[rule.id] ?? {};
    const currentFiles = current[rule.id] ?? {};
    const allFiles = new Set([...Object.keys(baselineFiles), ...Object.keys(currentFiles)]);

    for (const file of [...allFiles].sort()) {
      const previous = baselineFiles[file] ?? 0;
      const next = currentFiles[file] ?? 0;

      if (next > previous) {
        failures.push(`${rule.id}: ${file} increased ${previous} -> ${next}`);
      } else if (previous === 0 && next > 0) {
        failures.push(`${rule.id}: ${file} introduced ${next} violation(s)`);
      } else if (next < previous) {
        improvements.push(`${rule.id}: ${file} decreased ${previous} -> ${next}`);
      }
    }
  }

  return { failures, improvements };
}

async function main() {
  const writeBaseline = process.argv.includes("--write-baseline");
  const current = sortObject(await collectCounts());

  if (writeBaseline) {
    await writeFile(`${baselinePath}`, `${JSON.stringify(current, null, 2)}\n`, "utf8");
    console.log(`Wrote frontend invariant baseline to ${path.relative(repoRoot, baselinePath)}`);
    return;
  }

  let baseline;
  try {
    baseline = JSON.parse(await readFile(baselinePath, "utf8"));
  } catch (error) {
    console.error(`Missing or invalid baseline at ${path.relative(repoRoot, baselinePath)}`);
    console.error("Run: node scripts/check-frontend-invariants.mjs --write-baseline");
    throw error;
  }

  const { failures, improvements } = compareCounts(baseline, current);

  if (improvements.length > 0) {
    console.log("Frontend invariant debt improved:");
    for (const line of improvements) {
      console.log(`  - ${line}`);
    }
  }

  if (failures.length > 0) {
    console.error("Frontend invariant check failed:");
    for (const line of failures) {
      console.error(`  - ${line}`);
    }
    process.exitCode = 1;
    return;
  }

  console.log("Frontend invariant check passed. No new violations beyond the checked-in baseline.");
}

await main();
