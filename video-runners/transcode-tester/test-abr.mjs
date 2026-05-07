#!/usr/bin/env node

/**
 * ABR transcoding integration test.
 *
 * Usage:
 *   node test-abr.mjs presets          — list available ABR presets
 *   node test-abr.mjs quick            — submit an ABR job, poll per-rendition status
 *
 * Environment:
 *   PROXY_URL   — proxy base URL          (default: http://localhost:8090)
 *   INPUT_URL   — pre-signed GET URL for source video
 *   MANIFEST_URL — pre-signed PUT URL for master.m3u8
 *   RENDITION_URLS — JSON map, e.g. '{"1080p":{"playlist":"...","stream":"..."},...}'
 *   ABR_PRESET  — preset name              (default: abr-standard)
 */

const PROXY = process.env.PROXY_URL || "http://localhost:8090";
const MODE = process.argv[2] || "presets";

async function post(path, body) {
  const res = await fetch(`${PROXY}${path}`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  const text = await res.text();
  try {
    return { status: res.status, data: JSON.parse(text) };
  } catch {
    return { status: res.status, data: text };
  }
}

async function get(path) {
  const res = await fetch(`${PROXY}${path}`);
  const text = await res.text();
  try {
    return { status: res.status, data: JSON.parse(text) };
  } catch {
    return { status: res.status, data: text };
  }
}

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

// ── Presets mode ──

async function testPresets() {
  console.log("Fetching ABR presets...\n");
  const { status, data } = await get("/v1/video/transcode/abr/presets");
  console.log(`Status: ${status}`);
  if (status !== 200) {
    console.error("Failed to fetch presets:", data);
    process.exit(1);
  }
  console.log(`GPU: ${data.gpu}`);
  console.log(`Presets: ${data.count}\n`);

  for (const p of data.presets) {
    console.log(`  [${p.name}] ${p.description}`);
    console.log(`    Renditions: ${p.renditions.map((r) => r.name).join(", ")}`);
    console.log(`    Segment duration: ${p.segment_duration}s`);
    console.log();
  }
  console.log("Presets test passed.");
}

// ── Quick mode ──

async function testQuick() {
  const inputURL = process.env.INPUT_URL;
  const manifestURL = process.env.MANIFEST_URL;
  const renditionURLsJSON = process.env.RENDITION_URLS;
  const presetName = process.env.ABR_PRESET || "abr-standard";

  if (!inputURL || !manifestURL || !renditionURLsJSON) {
    console.error(
      "Quick mode requires INPUT_URL, MANIFEST_URL, and RENDITION_URLS environment variables."
    );
    console.error(
      'RENDITION_URLS should be JSON like: \'{"1080p":{"playlist":"...","stream":"..."},...}\''
    );
    process.exit(1);
  }

  let renditionURLs;
  try {
    renditionURLs = JSON.parse(renditionURLsJSON);
  } catch (e) {
    console.error("RENDITION_URLS is not valid JSON:", e.message);
    process.exit(1);
  }

  console.log(`Submitting ABR job with preset: ${presetName}`);
  console.log(`Input: ${inputURL.substring(0, 80)}...`);
  console.log(`Renditions: ${Object.keys(renditionURLs).join(", ")}\n`);

  const { status, data } = await post("/v1/video/transcode/abr", {
    input_url: inputURL,
    output_urls: {
      manifest: manifestURL,
      renditions: renditionURLs,
    },
    preset: presetName,
  });

  if (status !== 202) {
    console.error(`Submit failed (HTTP ${status}):`, data);
    process.exit(1);
  }

  const jobId = data.job_id;
  console.log(`Job submitted: ${jobId}`);
  console.log(`Renditions: ${data.renditions.join(", ")}\n`);

  // Poll until complete or error
  const startTime = Date.now();
  const timeout = 30 * 60 * 1000; // 30 minutes

  while (Date.now() - startTime < timeout) {
    await sleep(3000);

    const { data: st } = await post("/v1/video/transcode/abr/status", {
      job_id: jobId,
    });

    const elapsed = ((Date.now() - startTime) / 1000).toFixed(0);

    // Show per-rendition progress
    const rendSummary = st.renditions
      .map((r) => {
        const pct = r.progress.toFixed(1);
        const fps = r.encoding_fps > 0 ? ` ${r.encoding_fps.toFixed(0)}fps` : "";
        const speed = r.speed || "";
        return `${r.name}:${r.status}(${pct}%${fps}${speed ? " " + speed : ""})`;
      })
      .join(" | ");

    console.log(
      `[${elapsed}s] ${st.phase} overall=${st.overall_progress.toFixed(1)}% ${rendSummary}`
    );

    if (st.status === "complete") {
      console.log(`\nJob complete in ${st.processing_time_seconds.toFixed(1)}s`);
      console.log(`GPU: ${st.gpu}`);
      if (st.input) {
        console.log(
          `Input: ${st.input.width}x${st.input.height} ${st.input.video_codec} ${st.input.duration.toFixed(1)}s`
        );
      }
      for (const r of st.renditions) {
        const size = r.file_size
          ? `${(r.file_size / 1024 / 1024).toFixed(1)}MB`
          : "?";
        console.log(
          `  ${r.name}: bitrate=${r.bitrate || "?"} size=${size}`
        );
      }
      console.log("\nQuick test passed.");
      return;
    }

    if (st.status === "error") {
      console.error(`\nJob failed: ${st.error} (${st.error_code})`);
      process.exit(1);
    }
  }

  console.error("Timeout waiting for job completion");
  process.exit(1);
}

// ── Main ──

switch (MODE) {
  case "presets":
    await testPresets();
    break;
  case "quick":
    await testQuick();
    break;
  default:
    console.error(`Unknown mode: ${MODE}. Use "presets" or "quick".`);
    process.exit(1);
}
