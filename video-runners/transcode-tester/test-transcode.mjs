#!/usr/bin/env node

/**
 * Transcode runner integration test.
 *
 * Submits a transcode job, polls for progress, and validates the result.
 *
 * Usage:
 *   node test-transcode.mjs quick     # submit + poll + validate (~1-2 min)
 *   node test-transcode.mjs presets   # list available presets
 *
 * Environment:
 *   OPENAI_BASE_URL    - proxy base URL (default: http://localhost:8090/v1)
 *   POLL_INTERVAL_MS   - polling interval in ms (default: 3000)
 *   INPUT_URL          - URL to a test video file (default: Big Buck Bunny 10s clip)
 *   OUTPUT_URL         - pre-signed URL for upload (default: none, job will fail at upload)
 *   PRESET             - preset name to use (default: h264-720p)
 */

const proxyURL = process.env.OPENAI_BASE_URL || "http://localhost:8090/v1";
const submitEndpoint = `${proxyURL}/video/transcode`;
const statusEndpoint = `${proxyURL}/video/transcode/status`;
const presetsEndpoint = `${proxyURL}/video/transcode/presets`;
const pollIntervalMs = parseInt(process.env.POLL_INTERVAL_MS || "3000", 10);
const mode = process.argv[2] || "quick";

// Default test input: Big Buck Bunny 10-second clip (public URL, freely licensed)
const defaultInputURL =
  "https://test-videos.co.uk/vids/bigbuckbunny/mp4/h264/720/Big_Buck_Bunny_720_10s_1MB.mp4";

const inputURL = process.env.INPUT_URL || defaultInputURL;
const outputURL = process.env.OUTPUT_URL || "";
const preset = process.env.PRESET || "h264-720p";

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

const failures = [];

function assert(condition, message) {
  if (!condition) {
    console.error(`  ASSERTION FAILED: ${message}`);
    failures.push(message);
  }
}

// ---------------------------------------------------------------------------
// Test: List presets
// ---------------------------------------------------------------------------

async function testPresets() {
  console.log("=== Transcode Presets Test ===");
  console.log(`  Endpoint: ${presetsEndpoint}`);
  console.log();

  const response = await fetch(presetsEndpoint);
  if (!response.ok) {
    const text = await response.text();
    console.error(`Error: HTTP ${response.status} — ${text}`);
    process.exit(1);
  }

  const result = await response.json();
  console.log(`GPU:     ${result.gpu || "none"}`);
  console.log(`Presets: ${result.count}`);
  console.log();

  if (result.presets && result.presets.length > 0) {
    console.log("Available presets:");
    for (const p of result.presets) {
      const encoder = p.gpu_required ? "GPU" : "CPU";
      console.log(
        `  [${p.name}] ${p.video_codec} ${p.width}x${p.height} @ ${p.bitrate} (${encoder})`,
      );
    }
  }

  assert(result.count > 0, "at least one preset should be available");

  console.log();
  if (failures.length > 0) {
    console.error(`=== FAILED (${failures.length} assertion failure(s)) ===`);
    process.exit(1);
  }
  console.log("=== PASSED ===");
}

// ---------------------------------------------------------------------------
// Test: Quick submit + poll
// ---------------------------------------------------------------------------

async function testQuick() {
  console.log("=== Transcode Quick Test ===");
  console.log(`  Submit:    ${submitEndpoint}`);
  console.log(`  Status:    ${statusEndpoint}`);
  console.log(`  Input:     ${inputURL}`);
  console.log(`  Output:    ${outputURL || "(none — will test up to encoding)"}`);
  console.log(`  Preset:    ${preset}`);
  console.log(`  Poll:      ${pollIntervalMs}ms`);
  console.log();

  if (!outputURL) {
    console.log(
      "NOTE: No OUTPUT_URL set. Job will complete encoding but fail at upload.",
    );
    console.log(
      "      Set OUTPUT_URL to a pre-signed PUT URL for full end-to-end test.",
    );
    console.log();
  }

  // Step 1: Submit
  console.log("Submitting job...");
  const body = {
    input_url: inputURL,
    output_url: outputURL || "https://example.com/output.mp4",
    preset: preset,
  };

  const submitResponse = await fetch(submitEndpoint, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

  if (!submitResponse.ok) {
    const text = await submitResponse.text();
    console.error(`Error: HTTP ${submitResponse.status} — ${text}`);
    process.exit(1);
  }

  const submitResult = await submitResponse.json();
  const jobId = submitResult.job_id;

  assert(!!jobId, "job_id should be present in response");
  assert(
    submitResult.status === "queued",
    `status should be "queued", got "${submitResult.status}"`,
  );

  console.log(`  Job ID: ${jobId}`);
  console.log(`  Status: ${submitResult.status}`);
  console.log();

  if (!jobId) {
    console.error("Cannot continue without job_id");
    process.exit(1);
  }

  // Step 2: Poll
  const startTime = Date.now();
  const maxPollTime = 5 * 60 * 1000; // 5 minutes max
  let lastPhase = "";
  let done = false;
  let hadError = false;

  while (!done && Date.now() - startTime < maxPollTime) {
    await sleep(pollIntervalMs);

    let statusResult;
    try {
      const statusResponse = await fetch(statusEndpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ job_id: jobId }),
      });

      if (!statusResponse.ok) {
        const text = await statusResponse.text();
        console.error(`  Poll error: HTTP ${statusResponse.status} — ${text}`);
        continue;
      }

      statusResult = await statusResponse.json();
    } catch (err) {
      console.error(`  Poll error: ${err.message}`);
      continue;
    }

    const { phase, status, progress } = statusResult;

    // Print phase transitions
    if (phase && phase !== lastPhase) {
      console.log(`--- Phase: ${phase} ---`);
      lastPhase = phase;
    }

    // Print progress updates
    if (phase === "encoding" && progress !== undefined) {
      const fps = statusResult.encoding_fps || 0;
      const speed = statusResult.speed || "";
      console.log(
        `  Progress: ${progress.toFixed(1)}% | FPS: ${fps.toFixed(1)} | Speed: ${speed}`,
      );
    } else if (phase === "downloading" || phase === "uploading") {
      if (progress !== undefined) {
        console.log(`  Progress: ${progress.toFixed(1)}%`);
      }
    }

    // Check terminal states
    if (status === "complete") {
      console.log();
      console.log("--- Result ---");
      if (statusResult.input) {
        const inp = statusResult.input;
        console.log(
          `  Input:  ${inp.video_codec} ${inp.width}x${inp.height} @ ${inp.fps}fps, ${inp.duration.toFixed(1)}s`,
        );
      }
      if (statusResult.output) {
        const out = statusResult.output;
        console.log(
          `  Output: ${out.video_codec} ${out.width}x${out.height} @ ${out.fps}fps, ${(out.file_size / 1024 / 1024).toFixed(1)}MB`,
        );
      }
      console.log(`  GPU:    ${statusResult.gpu || "none"}`);
      console.log(
        `  Time:   ${statusResult.processing_time_seconds.toFixed(1)}s`,
      );

      // Validate
      assert(
        statusResult.input && statusResult.input.video_codec,
        "input should have video_codec detected",
      );

      done = true;
    } else if (status === "error") {
      console.log();
      console.error(`  ERROR: ${statusResult.error}`);
      console.error(`  Code:  ${statusResult.error_code}`);

      // If no output URL was set, upload error is expected
      if (!outputURL && statusResult.error_code === "UPLOAD_ERROR") {
        console.log();
        console.log(
          "  (Upload error is expected when no OUTPUT_URL is set)",
        );
        console.log("  Encoding phase completed successfully.");

        // Validate that we at least detected input
        assert(
          statusResult.input && statusResult.input.video_codec,
          "input should have video_codec detected before upload failure",
        );
        done = true;
      } else {
        hadError = true;
        done = true;
      }
    }
  }

  if (!done) {
    console.error("Timeout: job did not complete within 5 minutes");
    hadError = true;
  }

  // Done
  const elapsed = ((Date.now() - startTime) / 1000).toFixed(1);
  console.log();

  if (hadError || failures.length > 0) {
    console.error(
      `=== FAILED after ${elapsed}s` +
        (failures.length > 0
          ? ` (${failures.length} assertion failure(s))`
          : "") +
        ` ===`,
    );
    process.exit(1);
  } else {
    console.log(`=== PASSED in ${elapsed}s ===`);
    process.exit(0);
  }
}

// ---------------------------------------------------------------------------
// Run
// ---------------------------------------------------------------------------

try {
  if (mode === "presets") {
    await testPresets();
  } else if (mode === "quick") {
    await testQuick();
  } else {
    console.error(`Unknown mode: "${mode}". Use "quick" or "presets".`);
    process.exit(1);
  }
} catch (err) {
  console.error(`Error: ${err.message}`);
  process.exit(1);
}
