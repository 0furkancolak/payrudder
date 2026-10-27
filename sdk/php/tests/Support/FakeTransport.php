<?php

declare(strict_types=1);

namespace Payrudder\Tests\Support;

use Closure;
use LogicException;
use Payrudder\Exception\ConnectionException;
use Payrudder\Transport\HttpRequest;
use Payrudder\Transport\HttpResponse;
use Payrudder\Transport\TransportInterface;
use Throwable;

/**
 * Answers from a queue (or a callback) and records every request.
 */
final class FakeTransport implements TransportInterface
{
    /** @var list<HttpRequest> */
    public array $requests = [];

    /** @var list<HttpResponse|Throwable> */
    private array $queue = [];

    /** @var (Closure(HttpRequest):HttpResponse)|null */
    private ?Closure $handler = null;

    public function push(HttpResponse|Throwable ...$items): self
    {
        foreach ($items as $i) {
            $this->queue[] = $i;
        }

        return $this;
    }

    /**
     * @param Closure(HttpRequest):HttpResponse $handler
     */
    public function handle(Closure $handler): self
    {
        $this->handler = $handler;

        return $this;
    }

    /**
     * @param array<string,mixed>|string $body
     * @param array<string,string> $headers
     */
    public static function json(int $status, array|string $body = [], array $headers = []): HttpResponse
    {
        $text = \is_string($body) ? $body : json_encode($body, JSON_THROW_ON_ERROR);

        return new HttpResponse($status, array_change_key_case($headers + ['content-type' => 'application/json']), $text);
    }

    /**
     * @param array<string,string> $headers
     */
    public static function error(int $status, string $code, string $key, string $message = 'boom', array $headers = []): HttpResponse
    {
        return self::json($status, ['error' => ['code' => $code, 'key' => $key, 'message' => $message, 'request_id' => 'req_1']], $headers);
    }

    public function send(HttpRequest $request): HttpResponse
    {
        $this->requests[] = $request;
        if ($this->handler !== null) {
            return ($this->handler)($request);
        }
        $next = array_shift($this->queue);
        if ($next === null) {
            throw new ConnectionException('FakeTransport: no response queued');
        }
        if ($next instanceof Throwable) {
            throw $next;
        }

        return $next;
    }

    /**
     * @return array<string,mixed>
     */
    public static function decode(string $json): array
    {
        $data = json_decode($json, true, 512, JSON_THROW_ON_ERROR);

        return \is_array($data) ? self::stringKeys($data) : [];
    }

    /**
     * @param array<array-key,mixed> $in
     *
     * @return array<string,mixed>
     */
    public static function stringKeys(array $in): array
    {
        $out = [];
        foreach ($in as $k => $v) {
            $out[(string) $k] = $v;
        }

        return $out;
    }

    /**
     * @return array<string,mixed>
     */
    public function body(int $index = 0): array
    {
        return self::decode($this->requests[$index]->body ?? '');
    }

    /**
     * The events of a POST /v1/events request.
     *
     * @return list<array<string,mixed>>
     */
    public function events(int $index = 0): array
    {
        $events = $this->body($index)['events'] ?? [];
        $out = [];
        if (\is_array($events)) {
            foreach ($events as $e) {
                if (\is_array($e)) {
                    $out[] = self::stringKeys($e);
                }
            }
        }

        return $out;
    }

    /**
     * @return list<string>
     */
    public function eventKeys(int $index = 0): array
    {
        $keys = [];
        foreach ($this->events($index) as $e) {
            $k = $e['idempotency_key'] ?? null;
            if (!\is_string($k)) {
                throw new LogicException('event without an idempotency key');
            }
            $keys[] = $k;
        }

        return $keys;
    }
}
