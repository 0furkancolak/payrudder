<?php

declare(strict_types=1);

namespace Payrudder\Transport;

final class HttpResponse
{
    /**
     * @param array<string,string> $headers header names lower-cased, repeated headers joined with ", "
     */
    public function __construct(
        public readonly int $status,
        public readonly array $headers,
        public readonly string $body,
    ) {}

    public function header(string $name): ?string
    {
        return $this->headers[strtolower($name)] ?? null;
    }
}
