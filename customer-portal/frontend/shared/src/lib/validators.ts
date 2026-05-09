export function isValidEmail(value: string): boolean {
  if (typeof value !== 'string' || value.length === 0) return false;
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value);
}

export interface ValidationResult {
  ok: boolean;
  errors: Record<string, string>;
}

export function validateSignup(input: { email: string }): ValidationResult {
  const errors: Record<string, string> = {};
  if (!isValidEmail(input.email)) errors['email'] = 'invalid email';
  return { ok: Object.keys(errors).length === 0, errors };
}
