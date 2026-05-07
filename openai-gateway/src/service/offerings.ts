import fs from 'node:fs';
import yaml from 'js-yaml';
import { z } from 'zod';

const OfferingValueSchema = z.union([
  z.string(),
  z.record(z.string(), z.string()),
]);

const OfferingsFileSchema = z.object({
  defaults: z.record(z.string(), OfferingValueSchema).default({}),
});

export type OfferingValue = string | Record<string, string>;
export interface OfferingsConfig {
  defaults: Record<string, OfferingValue>;
}

export function parseOfferingsYaml(text: string): OfferingsConfig {
  const raw = yaml.load(text) ?? {};
  return OfferingsFileSchema.parse(raw);
}

export function loadOfferingsFromDisk(filePath: string): OfferingsConfig {
  if (!fs.existsSync(filePath)) {
    return { defaults: {} };
  }
  const text = fs.readFileSync(filePath, 'utf8');
  return parseOfferingsYaml(text);
}

export interface ResolveDefaultOfferingInput {
  capability: string;
  variant?: string;
}

export function resolveDefaultOffering(
  config: OfferingsConfig,
  input: ResolveDefaultOfferingInput,
): string | null {
  const entry = config.defaults[input.capability];
  if (!entry) return null;
  if (typeof entry === 'string') return entry;
  if (input.variant && entry[input.variant]) return entry[input.variant]!;
  if (entry['default']) return entry['default']!;
  const firstKey = Object.keys(entry)[0];
  return firstKey ? entry[firstKey]! : null;
}
