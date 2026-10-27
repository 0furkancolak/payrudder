<?php

declare(strict_types=1);

namespace Payrudder\Internal;

use BackedEnum;
use Payrudder\Contract\RequestBody;
use Payrudder\RequestOptions;

/**
 * @internal
 */
abstract class AbstractResource
{
    public function __construct(protected readonly Requester $http) {}

    /**
     * @param array<string,scalar|BackedEnum|null> $query
     * @param array<string,mixed>|null $body
     *
     * @return array<string,mixed>
     */
    protected function call(string $method, string $path, array $query, ?array $body, ?RequestOptions $options): array
    {
        return $this->http->json($method, $path, self::query($query), $body, $options);
    }

    /**
     * @param array<string,scalar|BackedEnum|null> $query
     * @param array<string,mixed>|null $body
     */
    protected function callVoid(string $method, string $path, array $query, ?array $body, ?RequestOptions $options): void
    {
        $this->http->void($method, $path, self::query($query), $body, $options);
    }

    /**
     * @param array<string,scalar|BackedEnum|null> $query
     * @param array<string,mixed>|null $body
     */
    protected function callRaw(string $method, string $path, array $query, ?array $body, ?RequestOptions $options): string
    {
        return $this->http->raw($method, $path, self::query($query), $body, $options);
    }

    /**
     * @param array<string,scalar|BackedEnum|null> $query
     *
     * @return array<string,scalar|null>
     */
    private static function query(array $query): array
    {
        $out = [];
        foreach ($query as $k => $v) {
            $out[$k] = $v instanceof BackedEnum ? $v->value : $v;
        }

        return $out;
    }

    /**
     * @param RequestBody|array<string,mixed> $body
     *
     * @return array<string,mixed>
     */
    protected static function body(RequestBody|array $body): array
    {
        return Cast::body($body);
    }
}
