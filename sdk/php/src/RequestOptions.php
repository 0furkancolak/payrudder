<?php

declare(strict_types=1);

namespace Payrudder;

/**
 * Per-call settings. Every field overrides the client's default for that one call.
 */
final class RequestOptions
{
    /**
     * @param string|null $idempotencyKey sent as `Idempotency-Key`; the same key is reused by every automatic retry
     * @param array<string,string> $headers extra request headers
     * @param float|null $timeout seconds
     * @param int|null $maxRetries 0 disables automatic retries
     */
    public function __construct(
        public readonly ?string $idempotencyKey = null,
        public readonly array $headers = [],
        public readonly ?float $timeout = null,
        public readonly ?int $maxRetries = null,
    ) {}
}
