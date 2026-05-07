import type { Db } from '../db/pool.js';
import * as apiKeysRepo from '../repo/apiKeys.js';
import { generateApiKey, hashApiKey, type EnvPrefix } from './apiKey.js';

export interface IssueKeyInput {
  customerId: string;
  envPrefix: EnvPrefix;
  pepper: string;
  label?: string;
}

export interface IssueKeyResult {
  apiKeyId: string;
  plaintext: string;
}

export async function issueKey(db: Db, input: IssueKeyInput): Promise<IssueKeyResult> {
  const plaintext = generateApiKey(input.envPrefix);
  const hash = hashApiKey(input.pepper, plaintext);
  const row = await apiKeysRepo.insertApiKey(db, {
    customerId: input.customerId,
    hash,
    ...(input.label !== undefined ? { label: input.label } : {}),
  });
  return { apiKeyId: row.id, plaintext };
}

export async function revokeKey(db: Db, apiKeyId: string): Promise<void> {
  await apiKeysRepo.revoke(db, apiKeyId, new Date());
}
