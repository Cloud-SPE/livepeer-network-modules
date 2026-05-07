export function isValidEmail(value: string): boolean {
  if (typeof value !== 'string' || value.length === 0) return false;
  return /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(value);
}

export function isStrongPassword(value: string): boolean {
  return typeof value === 'string' && value.length >= 8;
}

export interface ValidationResult {
  ok: boolean;
  errors: Record<string, string>;
}

export function validateSignup(input: { email: string; password: string }): ValidationResult {
  const errors: Record<string, string> = {};
  if (!isValidEmail(input.email)) errors['email'] = 'invalid email';
  if (!isStrongPassword(input.password)) errors['password'] = 'password must be at least 8 characters';
  return { ok: Object.keys(errors).length === 0, errors };
}
