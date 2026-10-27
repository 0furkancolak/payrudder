export interface ErrorDetail {
  field?: string;
  description?: string;
}

export interface PayrudderErrorInit {
  status: number;
  code?: string;
  key?: string;
  message: string;
  details?: ErrorDetail[];
  requestId?: string;
  /** Seconds, from the `Retry-After` header. */
  retryAfter?: number;
  body?: unknown;
  cause?: unknown;
}

/** Any failure of an API call. HTTP failures carry the API's `Error` envelope; `status` is 0 when no response arrived. */
export class PayrudderError extends Error {
  readonly status: number;
  /** Machine code of the envelope, e.g. `INVALID_ARGUMENT`. */
  readonly code: string | undefined;
  /** Stable, translatable key, e.g. `billing.credit_note.nothing_to_refund`. Translate the key, not the message. */
  readonly key: string | undefined;
  readonly details: ErrorDetail[];
  readonly requestId: string | undefined;
  readonly retryAfter: number | undefined;
  /** The parsed response body (or the raw text when it was not JSON). */
  readonly body: unknown;

  constructor(init: PayrudderErrorInit) {
    super(init.message, init.cause === undefined ? undefined : { cause: init.cause });
    this.name = new.target.name;
    this.status = init.status;
    this.code = init.code;
    this.key = init.key;
    this.details = init.details ?? [];
    this.requestId = init.requestId;
    this.retryAfter = init.retryAfter;
    this.body = init.body;
  }
}

/** 401: missing, malformed, revoked or expired API key. */
export class AuthenticationError extends PayrudderError {}
/** 403: the key lacks the permission, the tenant is not active, or a sandbox-only route was called with a live key. */
export class PermissionError extends PayrudderError {}
/** 404 */
export class NotFoundError extends PayrudderError {}
/** 409: already exists, or the resource is in a state that does not allow this. */
export class ConflictError extends PayrudderError {}
/** 400 and 422 */
export class ValidationError extends PayrudderError {}
/** 429. `retryAfter` is the server's hint in seconds. */
export class RateLimitError extends PayrudderError {}
/** 5xx */
export class ServerError extends PayrudderError {}
/** No usable response: DNS, refused connection, reset, or an aborted request. `status` is 0. */
export class ConnectionError extends PayrudderError {
  constructor(message: string, cause?: unknown) {
    super({ status: 0, message, cause });
  }
}
/** The request exceeded the configured timeout. */
export class TimeoutError extends ConnectionError {}

export function errorForStatus(status: number): new (init: PayrudderErrorInit) => PayrudderError {
  if (status === 401) return AuthenticationError;
  if (status === 403) return PermissionError;
  if (status === 404) return NotFoundError;
  if (status === 409) return ConflictError;
  if (status === 400 || status === 422) return ValidationError;
  if (status === 429) return RateLimitError;
  if (status >= 500) return ServerError;
  return PayrudderError;
}
