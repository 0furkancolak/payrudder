<?php

declare(strict_types=1);

namespace Payrudder\Exception;

use Throwable;

/**
 * The API answered with an error status. Carries the fields of the `Error` envelope.
 */
class ApiException extends PayrudderException
{
    /**
     * @param list<array{field?: string, description?: string}> $details
     */
    public function __construct(
        string $message,
        public readonly int $status,
        public readonly ?string $errorCode = null,
        public readonly ?string $errorKey = null,
        public readonly array $details = [],
        public readonly ?string $requestId = null,
        public readonly ?float $retryAfter = null,
        public readonly string $responseBody = '',
        ?Throwable $previous = null,
    ) {
        parent::__construct($message, $status, $previous);
    }

    /**
     * Builds the exception subtype that fits the status.
     *
     * @param array<string,string> $headers lower-cased header names
     */
    public static function fromResponse(int $status, array $headers, string $body): self
    {
        $code = null;
        $key = null;
        $message = null;
        $requestId = $headers['x-request-id'] ?? null;
        $details = [];

        $decoded = json_decode($body, true);
        if (\is_array($decoded) && isset($decoded['error']) && \is_array($decoded['error'])) {
            $err = $decoded['error'];
            $code = isset($err['code']) && \is_string($err['code']) ? $err['code'] : null;
            $key = isset($err['key']) && \is_string($err['key']) ? $err['key'] : null;
            $message = isset($err['message']) && \is_string($err['message']) ? $err['message'] : null;
            if (isset($err['request_id']) && \is_string($err['request_id']) && $err['request_id'] !== '') {
                $requestId = $err['request_id'];
            }
            if (isset($err['details']) && \is_array($err['details'])) {
                foreach ($err['details'] as $d) {
                    if (\is_array($d)) {
                        $item = [];
                        if (isset($d['field']) && \is_string($d['field'])) {
                            $item['field'] = $d['field'];
                        }
                        if (isset($d['description']) && \is_string($d['description'])) {
                            $item['description'] = $d['description'];
                        }
                        $details[] = $item;
                    }
                }
            }
        }
        $message ??= 'HTTP ' . $status;

        $retryAfter = null;
        if (isset($headers['retry-after'])) {
            $retryAfter = self::parseRetryAfter($headers['retry-after']);
        }

        $args = [$message, $status, $code, $key, $details, $requestId, $retryAfter, $body];

        return match (true) {
            $status === 401 => new AuthenticationException(...$args),
            $status === 403 => new PermissionException(...$args),
            $status === 404 => new NotFoundException(...$args),
            $status === 400 || $status === 422 => new ValidationException(...$args),
            $status === 409 => new ConflictException(...$args),
            $status === 429 => new RateLimitException(...$args),
            $status >= 500 => new ServerException(...$args),
            default => new self(...$args),
        };
    }

    /**
     * `Retry-After` is either a number of seconds or an HTTP date.
     */
    public static function parseRetryAfter(string $value, ?int $now = null): ?float
    {
        $value = trim($value);
        if ($value === '') {
            return null;
        }
        if (is_numeric($value)) {
            return max(0.0, (float) $value);
        }
        $at = strtotime($value);
        if ($at === false) {
            return null;
        }

        return max(0.0, (float) ($at - ($now ?? time())));
    }
}
