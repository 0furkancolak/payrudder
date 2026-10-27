<?php

declare(strict_types=1);

namespace Payrudder\Transport;

/**
 * One HTTP request, independent of the library that sends it.
 */
final class HttpRequest
{
    /**
     * @param array<string,string> $headers
     */
    public function __construct(
        public readonly string $method,
        public readonly string $url,
        public readonly array $headers,
        public readonly ?string $body,
        public readonly float $timeout,
    ) {}
}
