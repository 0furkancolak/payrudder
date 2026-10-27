<?php

declare(strict_types=1);

namespace Payrudder\Internal;

use Closure;
use JsonException;
use Payrudder\Exception\ApiException;
use Payrudder\Exception\ConnectionException;
use Payrudder\Exception\UnexpectedResponseException;
use Payrudder\RequestOptions;
use Payrudder\Transport\HttpRequest;
use Payrudder\Transport\HttpResponse;
use Payrudder\Transport\TransportInterface;

/**
 * Builds requests, retries them and maps error answers to exceptions.
 *
 * @internal
 */
final class Requester
{
    private const MAX_BACKOFF = 8.0;
    private const MAX_RETRY_AFTER = 60.0;

    /**
     * @param Closure(float):void $sleeper
     */
    public function __construct(
        private readonly TransportInterface $transport,
        private readonly string $baseUrl,
        private readonly string $apiKey,
        private readonly float $timeout,
        private readonly int $maxRetries,
        private readonly string $userAgent,
        private readonly Closure $sleeper,
    ) {}

    /**
     * A call with a JSON object (or no body) answered by a JSON object.
     *
     * @param array<string,scalar|null> $query
     * @param array<string,mixed>|null $body
     *
     * @return array<string,mixed>
     */
    public function json(string $method, string $path, array $query = [], ?array $body = null, ?RequestOptions $options = null, bool $idempotent = false): array
    {
        $payload = $body === null ? null : self::encode($body);
        $response = $this->send($method, $path, $query, $payload, $payload === null ? null : 'application/json', 'application/json', $options, $idempotent);
        if (trim($response->body) === '') {
            return [];
        }

        try {
            $data = json_decode($response->body, true, 512, JSON_BIGINT_AS_STRING | JSON_THROW_ON_ERROR);
        } catch (JsonException $e) {
            throw new UnexpectedResponseException('the API answered ' . $response->status . ' with invalid JSON: ' . $e->getMessage(), 0, $e);
        }
        if (!\is_array($data)) {
            throw new UnexpectedResponseException('the API answered ' . $response->status . ' with a JSON value that is not an object');
        }

        /** @var array<string,mixed> $data */
        return $data;
    }

    /**
     * @param array<string,scalar|null> $query
     * @param array<string,mixed>|null $body
     */
    public function void(string $method, string $path, array $query = [], ?array $body = null, ?RequestOptions $options = null): void
    {
        $payload = $body === null ? null : self::encode($body);
        $this->send($method, $path, $query, $payload, $payload === null ? null : 'application/json', '*/*', $options, false);
    }

    /**
     * @param array<string,scalar|null> $query
     * @param array<string,mixed>|null $body
     */
    public function raw(string $method, string $path, array $query = [], ?array $body = null, ?RequestOptions $options = null): string
    {
        $payload = $body === null ? null : self::encode($body);

        return $this->send($method, $path, $query, $payload, $payload === null ? null : 'application/json', '*/*', $options, false)->body;
    }

    /**
     * A call with a ready-made body (multipart uploads), answered by a JSON object.
     *
     * @param array<string,scalar|null> $query
     *
     * @return array<string,mixed>
     */
    public function jsonWithPayload(string $method, string $path, string $payload, string $contentType, array $query = [], ?RequestOptions $options = null): array
    {
        $response = $this->send($method, $path, $query, $payload, $contentType, 'application/json', $options, false);
        try {
            $data = json_decode($response->body, true, 512, JSON_BIGINT_AS_STRING | JSON_THROW_ON_ERROR);
        } catch (JsonException $e) {
            throw new UnexpectedResponseException('the API answered with invalid JSON: ' . $e->getMessage(), 0, $e);
        }
        if (!\is_array($data)) {
            throw new UnexpectedResponseException('the API answered with a JSON value that is not an object');
        }

        /** @var array<string,mixed> $data */
        return $data;
    }

    /**
     * @param array<array-key,mixed> $body
     */
    public static function encode(array $body): string
    {
        // An empty PHP array is the empty JSON object here: every body of this API is an object.
        if ($body === []) {
            return '{}';
        }

        return json_encode($body, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_PRESERVE_ZERO_FRACTION);
    }

    /**
     * @param array<string,scalar|null> $query
     */
    private function send(string $method, string $path, array $query, ?string $payload, ?string $contentType, string $accept, ?RequestOptions $options, bool $idempotent): HttpResponse
    {
        $maxRetries = $options->maxRetries ?? $this->maxRetries;
        $timeout = $options->timeout ?? $this->timeout;

        $headers = [
            'Authorization' => 'Bearer ' . $this->apiKey,
            'Accept' => $accept,
            'User-Agent' => $this->userAgent,
        ];
        if ($contentType !== null) {
            $headers['Content-Type'] = $contentType;
        }
        $key = $options?->idempotencyKey;
        if ($key !== null && $key !== '') {
            $headers['Idempotency-Key'] = $key;
        }
        foreach ($options->headers ?? [] as $name => $value) {
            $headers[$name] = $value;
        }

        $request = new HttpRequest($method, $this->url($path, $query), $headers, $payload, $timeout);
        $safeToRetry500 = $idempotent || $key !== null && $key !== '' || \in_array($method, ['GET', 'HEAD', 'PUT', 'DELETE', 'OPTIONS'], true);

        for ($attempt = 0;; ++$attempt) {
            try {
                $response = $this->transport->send($request);
            } catch (ConnectionException $e) {
                if ($attempt >= $maxRetries) {
                    throw $e;
                }
                ($this->sleeper)(self::backoff($attempt));

                continue;
            }

            if ($response->status >= 200 && $response->status < 300) {
                return $response;
            }

            $error = ApiException::fromResponse($response->status, $response->headers, $response->body);
            if ($attempt >= $maxRetries || !self::retryable($response->status, $safeToRetry500)) {
                throw $error;
            }
            $delay = $error->retryAfter;
            if ($delay !== null && $delay > self::MAX_RETRY_AFTER) {
                throw $error; // the server asks for longer than a call should block
            }
            ($this->sleeper)($delay ?? self::backoff($attempt));
        }
    }

    private static function retryable(int $status, bool $safeToRetry500): bool
    {
        return match ($status) {
            429, 502, 503, 504 => true,
            500 => $safeToRetry500,
            default => false,
        };
    }

    /**
     * Exponential backoff with jitter: 0.5 s, 1 s, 2 s, ... capped, each scaled by 50..100 %.
     */
    private static function backoff(int $attempt): float
    {
        $base = min(self::MAX_BACKOFF, 0.5 * (2 ** $attempt));

        return $base * (random_int(500, 1000) / 1000);
    }

    /**
     * @param array<string,scalar|null> $query
     */
    private function url(string $path, array $query): string
    {
        $pairs = [];
        foreach ($query as $name => $value) {
            if ($value === null) {
                continue;
            }
            if (\is_bool($value)) {
                $value = $value ? 'true' : 'false';
            }
            $pairs[] = rawurlencode($name) . '=' . rawurlencode((string) $value);
        }

        return $this->baseUrl . $path . ($pairs === [] ? '' : '?' . implode('&', $pairs));
    }
}
