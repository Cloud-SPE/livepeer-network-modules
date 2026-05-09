import { mkdir, readdir, readFile, rm, stat, writeFile, copyFile } from "node:fs/promises";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const __dirname = dirname(fileURLToPath(import.meta.url));
const packageRoot = resolve(__dirname, "..");
const srcCssDir = resolve(packageRoot, "src/css");
const distCssDir = resolve(packageRoot, "dist/css");

async function removeDirIfPresent(path) {
  await rm(path, { recursive: true, force: true });
}

async function walkCssFiles(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const fullPath = join(dir, entry.name);
    if (entry.isDirectory()) {
      files.push(...(await walkCssFiles(fullPath)));
      continue;
    }
    if (entry.isFile() && entry.name.endsWith(".css")) {
      files.push(fullPath);
    }
  }
  return files.sort();
}

async function copyCssTree() {
  const files = await walkCssFiles(srcCssDir);
  await mkdir(distCssDir, { recursive: true });
  for (const file of files) {
    const rel = relative(srcCssDir, file);
    const out = join(distCssDir, rel);
    await mkdir(dirname(out), { recursive: true });
    await copyFile(file, out);
  }
}

async function flattenImports(entryPath, seen = new Set()) {
  const resolved = resolve(entryPath);
  if (seen.has(resolved)) {
    return "";
  }
  seen.add(resolved);
  const source = await readFile(resolved, "utf8");
  const lines = source.split("\n");
  let output = "";
  for (const line of lines) {
    const match = line.match(/^@import url\("(.+)"\);$/);
    if (match) {
      output += await flattenImports(resolve(dirname(resolved), match[1]), seen);
      continue;
    }
    output += `${line}\n`;
  }
  return output;
}

async function writeBundledGlobalCss() {
  const bundled = await flattenImports(resolve(srcCssDir, "global.css"));
  await writeFile(join(distCssDir, "global.css"), bundled, "utf8");
}

await removeDirIfPresent(distCssDir);
await copyCssTree();
await writeBundledGlobalCss();
