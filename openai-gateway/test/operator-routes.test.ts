import test from 'node:test';
import assert from 'node:assert/strict';

import Fastify from 'fastify';
import type { AdminAuthResolver, AdminAuthResolverRequest } from '@livepeer-network-modules/customer-portal/auth';

import { registerOperatorRoutes } from '../src/routes/operator.js';
import type { RateCardSnapshot } from '../src/service/pricing/types.js';
import type { RouteSelector } from '../src/service/routeSelector.js';

test('operator routes expose resolver candidates and replace rate card snapshot', async (t) => {
  const authResolver: AdminAuthResolver = {
    async resolve(req: AdminAuthResolverRequest) {
      return req.headers.authorization === 'Bearer good' && req.headers['x-actor'] === 'ops@example.com'
        ? { actor: 'ops@example.com' }
        : null;
    },
  };

  let snapshot: RateCardSnapshot = {
    chatTiers: [{ tier: 'starter', inputUsdPerMillion: 1, outputUsdPerMillion: 2 }],
    chatModels: [{ modelOrPattern: 'gpt-*', isPattern: true, tier: 'starter', sortOrder: 10 }],
    embeddings: [],
    audioSpeech: [],
    audioTranscripts: [],
    images: [],
  };

  const store = createMockRateCardStore(() => snapshot, (next) => {
    snapshot = next;
  });

  const routeSelector: RouteSelector = {
    async select() {
      return [];
    },
    async inspect() {
      return [
        {
          brokerUrl: 'https://broker.example.com',
          capability: 'openai:chat-completions',
          offering: 'gpt-4o-mini',
          model: 'gpt-4o-mini',
          interactionMode: 'http-reqresp@v0',
          ethAddress: '0xabc',
          pricePerWorkUnitWei: '42',
          workUnit: 'million_input_tokens',
          extra: null,
          constraints: null,
        },
      ];
    },
    recordOutcome() {},
    inspectHealth() {
      return [
        {
          key: 'https://broker.example.com|0xabc|openai:chat-completions|gpt-4o-mini|http-reqresp@v0',
          consecutiveFailures: 2,
          coolingDown: true,
          cooldownUntil: 123456,
          lastFailureAt: 111111,
          lastFailureReason: 'broker_503',
          lastSuccessAt: 100000,
        },
      ];
    },
    inspectMetrics() {
      return {
        attemptsTotal: 3,
        successesTotal: 1,
        retryableFailuresTotal: 2,
        nonRetryableFailuresTotal: 0,
        cooldownsOpenedTotal: 1,
      };
    },
  };

  const app = Fastify();
  registerOperatorRoutes(app, {
    authResolver,
    rateCardStore: store as any,
    routeSelector,
  });
  t.after(async () => app.close());

  const unauthorized = await app.inject({
    method: 'GET',
    url: '/admin/openai/rate-card',
  });
  assert.equal(unauthorized.statusCode, 401);

  const getRateCard = await app.inject({
    method: 'GET',
    url: '/admin/openai/rate-card',
    headers: { authorization: 'Bearer good', 'x-actor': 'ops@example.com' },
  });
  assert.equal(getRateCard.statusCode, 200);
  assert.equal(getRateCard.json().chatTiers[0].tier, 'starter');

  const getCandidates = await app.inject({
    method: 'GET',
    url: '/admin/openai/resolver-candidates',
    headers: { authorization: 'Bearer good', 'x-actor': 'ops@example.com' },
  });
  assert.equal(getCandidates.statusCode, 200);
  assert.equal(getCandidates.json().candidates[0].offering, 'gpt-4o-mini');
  assert.deepEqual(getCandidates.json().summary, {
    tracked_routes: 1,
    cooling_routes: 1,
    routes_with_failures: 1,
    latest_failure_at: 111111,
    latest_success_at: 100000,
  });
  assert.deepEqual(getCandidates.json().metrics, {
    attemptsTotal: 3,
    successesTotal: 1,
    retryableFailuresTotal: 2,
    nonRetryableFailuresTotal: 0,
    cooldownsOpenedTotal: 1,
  });

  const getProm = await app.inject({
    method: 'GET',
    url: '/admin/openai/route-health/metrics',
    headers: { authorization: 'Bearer good', 'x-actor': 'ops@example.com' },
  });
  assert.equal(getProm.statusCode, 200);
  assert.match(getProm.headers['content-type'] ?? '', /^text\/plain/);
  assert.match(getProm.body, /livepeer_gateway_route_health_attempts_total\{gateway="openai"\} 3/);
  assert.match(getProm.body, /livepeer_gateway_route_health_cooling_routes\{gateway="openai"\} 1/);

  const nextSnapshot: RateCardSnapshot = {
    chatTiers: [{ tier: 'pro', inputUsdPerMillion: 3, outputUsdPerMillion: 4 }],
    chatModels: [{ modelOrPattern: 'gpt-4.1', isPattern: false, tier: 'pro', sortOrder: 1 }],
    embeddings: [{ modelOrPattern: 'text-embedding-3-small', isPattern: false, usdPerMillionTokens: 0.25, sortOrder: 1 }],
    audioSpeech: [],
    audioTranscripts: [],
    images: [{ modelOrPattern: 'gpt-image-1', isPattern: false, size: '1024x1024', quality: 'standard', usdPerImage: 0.19, sortOrder: 1 }],
  };
  const replace = await app.inject({
    method: 'PUT',
    url: '/admin/openai/rate-card',
    headers: {
      authorization: 'Bearer good',
      'x-actor': 'ops@example.com',
      'content-type': 'application/json',
    },
    payload: JSON.stringify(nextSnapshot),
  });
  assert.equal(replace.statusCode, 204);

  const getUpdated = await app.inject({
    method: 'GET',
    url: '/admin/openai/rate-card',
    headers: { authorization: 'Bearer good', 'x-actor': 'ops@example.com' },
  });
  const updated = getUpdated.json() as RateCardSnapshot;
  assert.equal(updated.chatTiers[0]?.tier, 'pro');
  assert.equal(updated.images[0]?.modelOrPattern, 'gpt-image-1');
});

function createMockRateCardStore(
  readSnapshot: () => RateCardSnapshot,
  writeSnapshot: (next: RateCardSnapshot) => void,
): {
  query: (sql: string, args?: unknown[]) => Promise<{ rows: Record<string, unknown>[] }>;
  connect: () => Promise<{
    query: (sql: string, args?: unknown[]) => Promise<{ rows: Record<string, unknown>[] }>;
    release: () => void;
  }>;
} {
  const query = async (sql: string, args: unknown[] = []): Promise<{ rows: Record<string, unknown>[] }> => {
    const snapshot = readSnapshot();
    if (sql.startsWith('SELECT tier')) {
      return {
        rows: snapshot.chatTiers.map((row) => ({
          tier: row.tier,
          input_usd_per_million: String(row.inputUsdPerMillion),
          output_usd_per_million: String(row.outputUsdPerMillion),
        })),
      };
    }
    if (sql.startsWith('SELECT model_or_pattern, is_pattern, tier, sort_order FROM app.rate_card_chat_models')) {
      return { rows: snapshot.chatModels.map((row) => ({ model_or_pattern: row.modelOrPattern, is_pattern: row.isPattern, tier: row.tier, sort_order: row.sortOrder })) };
    }
    if (sql.startsWith('SELECT model_or_pattern, is_pattern, usd_per_million_tokens::text, sort_order FROM app.rate_card_embeddings')) {
      return { rows: snapshot.embeddings.map((row) => ({ model_or_pattern: row.modelOrPattern, is_pattern: row.isPattern, usd_per_million_tokens: String(row.usdPerMillionTokens), sort_order: row.sortOrder })) };
    }
    if (sql.startsWith('SELECT model_or_pattern, is_pattern, usd_per_million_chars::text, sort_order FROM app.rate_card_audio_speech')) {
      return { rows: snapshot.audioSpeech.map((row) => ({ model_or_pattern: row.modelOrPattern, is_pattern: row.isPattern, usd_per_million_chars: String(row.usdPerMillionChars), sort_order: row.sortOrder })) };
    }
    if (sql.startsWith('SELECT model_or_pattern, is_pattern, usd_per_minute::text, sort_order FROM app.rate_card_audio_transcripts')) {
      return { rows: snapshot.audioTranscripts.map((row) => ({ model_or_pattern: row.modelOrPattern, is_pattern: row.isPattern, usd_per_minute: String(row.usdPerMinute), sort_order: row.sortOrder })) };
    }
    if (sql.startsWith('SELECT model_or_pattern, is_pattern, size, quality, usd_per_image::text, sort_order FROM app.rate_card_images')) {
      return { rows: snapshot.images.map((row) => ({ model_or_pattern: row.modelOrPattern, is_pattern: row.isPattern, size: row.size, quality: row.quality, usd_per_image: String(row.usdPerImage), sort_order: row.sortOrder })) };
    }

    const mutable = cloneSnapshot(readSnapshot());
    if (sql.startsWith('DELETE FROM app.rate_card_chat_models')) mutable.chatModels = [];
    else if (sql.startsWith('DELETE FROM app.rate_card_chat_tiers')) mutable.chatTiers = [];
    else if (sql.startsWith('DELETE FROM app.rate_card_embeddings')) mutable.embeddings = [];
    else if (sql.startsWith('DELETE FROM app.rate_card_audio_speech')) mutable.audioSpeech = [];
    else if (sql.startsWith('DELETE FROM app.rate_card_audio_transcripts')) mutable.audioTranscripts = [];
    else if (sql.startsWith('DELETE FROM app.rate_card_images')) mutable.images = [];
    else if (sql.startsWith('INSERT INTO app.rate_card_chat_tiers')) {
      mutable.chatTiers.push({ tier: args[0] as any, inputUsdPerMillion: Number(args[1]), outputUsdPerMillion: Number(args[2]) });
    } else if (sql.startsWith('INSERT INTO app.rate_card_chat_models')) {
      mutable.chatModels.push({ modelOrPattern: String(args[0]), isPattern: Boolean(args[1]), tier: args[2] as any, sortOrder: Number(args[3]) });
    } else if (sql.startsWith('INSERT INTO app.rate_card_embeddings')) {
      mutable.embeddings.push({ modelOrPattern: String(args[0]), isPattern: Boolean(args[1]), usdPerMillionTokens: Number(args[2]), sortOrder: Number(args[3]) });
    } else if (sql.startsWith('INSERT INTO app.rate_card_audio_speech')) {
      mutable.audioSpeech.push({ modelOrPattern: String(args[0]), isPattern: Boolean(args[1]), usdPerMillionChars: Number(args[2]), sortOrder: Number(args[3]) });
    } else if (sql.startsWith('INSERT INTO app.rate_card_audio_transcripts')) {
      mutable.audioTranscripts.push({ modelOrPattern: String(args[0]), isPattern: Boolean(args[1]), usdPerMinute: Number(args[2]), sortOrder: Number(args[3]) });
    } else if (sql.startsWith('INSERT INTO app.rate_card_images')) {
      mutable.images.push({ modelOrPattern: String(args[0]), isPattern: Boolean(args[1]), size: String(args[2]), quality: args[3] as any, usdPerImage: Number(args[4]), sortOrder: Number(args[5]) });
    }
    if (!sql.startsWith('SELECT ') && !sql.startsWith('BEGIN') && !sql.startsWith('COMMIT') && !sql.startsWith('ROLLBACK')) {
      writeSnapshot(mutable);
    }
    return { rows: [] };
  };

  return {
    query,
    async connect() {
      return {
        query,
        release() {},
      };
    },
  };
}

function cloneSnapshot(snapshot: RateCardSnapshot): RateCardSnapshot {
  return {
    chatTiers: [...snapshot.chatTiers],
    chatModels: [...snapshot.chatModels],
    embeddings: [...snapshot.embeddings],
    audioSpeech: [...snapshot.audioSpeech],
    audioTranscripts: [...snapshot.audioTranscripts],
    images: [...snapshot.images],
  };
}
