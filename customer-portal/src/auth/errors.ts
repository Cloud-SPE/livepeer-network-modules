export class MalformedAuthorizationError extends Error {
  readonly name = 'MalformedAuthorizationError';
  constructor(reason: string) {
    super(`malformed authorization header: ${reason}`);
  }
}

export class InvalidApiKeyError extends Error {
  readonly name = 'InvalidApiKeyError';
  constructor() {
    super('api key is invalid or revoked');
  }
}

export class AccountSuspendedError extends Error {
  readonly name = 'AccountSuspendedError';
  constructor(public readonly customerId: string) {
    super(`customer ${customerId} is suspended`);
  }
}

export class AccountClosedError extends Error {
  readonly name = 'AccountClosedError';
  constructor(public readonly customerId: string) {
    super(`customer ${customerId} is closed`);
  }
}
