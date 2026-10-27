<?php

declare(strict_types=1);

namespace Payrudder\Internal;

use BackedEnum;
use DateTimeInterface;
use Payrudder\Contract\Model;
use Payrudder\Contract\RequestBody;
use Payrudder\Exception\UnexpectedResponseException;
use stdClass;

/**
 * Lenient conversions from decoded JSON to typed values, and back.
 *
 * int64 values (`*_minor`) travel as JSON strings. They become PHP ints (64-bit on every
 * supported platform); a value that does not fit is an error, never a silent float.
 *
 * @internal
 */
final class Cast
{
    public static function string(mixed $v): ?string
    {
        if (\is_string($v)) {
            return $v;
        }
        if (\is_int($v) || \is_float($v)) {
            return (string) $v;
        }

        return null;
    }

    public static function int(mixed $v): ?int
    {
        if (\is_int($v)) {
            return $v;
        }
        if (\is_string($v) && $v !== '') {
            $i = filter_var($v, FILTER_VALIDATE_INT);
            if ($i !== false) {
                return $i;
            }
            if (preg_match('/^-?\d+$/', $v) === 1) {
                throw new UnexpectedResponseException("integer {$v} does not fit into a PHP int");
            }

            return null;
        }
        if (\is_float($v) && floor($v) === $v && abs($v) < 9.2e18) {
            return (int) $v;
        }

        return null;
    }

    public static function float(mixed $v): ?float
    {
        if (\is_int($v) || \is_float($v)) {
            return (float) $v;
        }
        if (\is_string($v) && is_numeric($v)) {
            return (float) $v;
        }

        return null;
    }

    public static function bool(mixed $v): ?bool
    {
        if (\is_bool($v)) {
            return $v;
        }
        if ($v === 'true') {
            return true;
        }
        if ($v === 'false') {
            return false;
        }

        return null;
    }

    public static function scalar(mixed $v): string|int|float|null
    {
        return \is_string($v) || \is_int($v) || \is_float($v) ? $v : null;
    }

    /**
     * @template T of \BackedEnum
     *
     * @param class-string<T> $class
     *
     * @return T|string|null the enum case, or the raw string for a value this SDK does not know yet
     */
    public static function enum(string $class, mixed $v): BackedEnum|string|null
    {
        $s = self::string($v);
        if ($s === null) {
            return null;
        }

        return $class::tryFrom($s) ?? $s;
    }

    /**
     * @template T of Model
     *
     * @param class-string<T> $class
     *
     * @return T|null
     */
    public static function model(string $class, mixed $v): ?Model
    {
        if (!\is_array($v)) {
            return null;
        }
        /** @var array<string,mixed> $v */
        return $class::fromArray($v);
    }

    /**
     * @template T of Model
     *
     * @param class-string<T> $class
     *
     * @return list<T>|null
     */
    public static function models(string $class, mixed $v): ?array
    {
        if (!\is_array($v)) {
            return null;
        }
        $out = [];
        foreach ($v as $item) {
            if (\is_array($item)) {
                /** @var array<string,mixed> $item */
                $out[] = $class::fromArray($item);
            }
        }

        return $out;
    }

    /**
     * @template T of \BackedEnum
     *
     * @param class-string<T> $class
     *
     * @return list<T|string>|null
     */
    public static function enums(string $class, mixed $v): ?array
    {
        if (!\is_array($v)) {
            return null;
        }
        $out = [];
        foreach ($v as $item) {
            $e = self::enum($class, $item);
            if ($e !== null) {
                $out[] = $e;
            }
        }

        return $out;
    }

    /**
     * @return list<string>|null
     */
    public static function strings(mixed $v): ?array
    {
        if (!\is_array($v)) {
            return null;
        }
        $out = [];
        foreach ($v as $item) {
            $s = self::string($item);
            if ($s !== null) {
                $out[] = $s;
            }
        }

        return $out;
    }

    /**
     * @return list<int>|null
     */
    public static function ints(mixed $v): ?array
    {
        if (!\is_array($v)) {
            return null;
        }
        $out = [];
        foreach ($v as $item) {
            $i = self::int($item);
            if ($i !== null) {
                $out[] = $i;
            }
        }

        return $out;
    }

    /**
     * @return list<mixed>|null
     */
    public static function list(mixed $v): ?array
    {
        return \is_array($v) ? array_values($v) : null;
    }

    /**
     * @return array<string,mixed>|null
     */
    public static function map(mixed $v): ?array
    {
        if (!\is_array($v)) {
            return null;
        }
        /** @var array<string,mixed> $v */
        return $v;
    }

    /**
     * @return array<string,string>|null
     */
    public static function stringMap(mixed $v): ?array
    {
        if (!\is_array($v)) {
            return null;
        }
        $out = [];
        foreach ($v as $k => $item) {
            $s = self::string($item);
            if ($s !== null) {
                $out[(string) $k] = $s;
            }
        }

        return $out;
    }

    /**
     * @return array<string,int>|null
     */
    public static function intMap(mixed $v): ?array
    {
        if (!\is_array($v)) {
            return null;
        }
        $out = [];
        foreach ($v as $k => $item) {
            $i = self::int($item);
            if ($i !== null) {
                $out[(string) $k] = $i;
            }
        }

        return $out;
    }

    /**
     * @param RequestBody|array<string,mixed> $body
     *
     * @return array<string,mixed>
     */
    public static function body(RequestBody|array $body): array
    {
        if ($body instanceof RequestBody) {
            return $body->toArray();
        }
        $out = [];
        foreach ($body as $k => $v) {
            $out[(string) $k] = self::wire($v);
        }

        return $out;
    }

    /**
     * Turns models, enums and dates into their JSON form.
     */
    public static function wire(mixed $v): mixed
    {
        if ($v instanceof RequestBody) {
            return $v->toArray();
        }
        if ($v instanceof BackedEnum) {
            return $v->value;
        }
        if ($v instanceof DateTimeInterface) {
            return $v->format(DateTimeInterface::RFC3339_EXTENDED);
        }
        if (\is_array($v)) {
            return array_map(self::wire(...), $v);
        }

        return $v;
    }

    /**
     * A map as a JSON object: an empty one must not turn into `[]`.
     *
     * @param array<array-key,mixed>|null $v
     */
    public static function wireMap(?array $v): mixed
    {
        if ($v === null) {
            return null;
        }
        if ($v === []) {
            return new stdClass();
        }

        return self::wire($v);
    }

    /**
     * int64 values go out as JSON strings, as the API renders them.
     */
    public static function int64(int|string|null $v): ?string
    {
        return $v === null ? null : (string) $v;
    }

    /**
     * @param list<int|string>|null $v
     *
     * @return list<string>|null
     */
    public static function int64List(?array $v): ?array
    {
        return $v === null ? null : array_map(static fn(int|string $i): string => (string) $i, $v);
    }

    /**
     * Drops the entries that are null (unset optional fields).
     *
     * @param array<string,mixed> $map
     *
     * @return array<string,mixed>
     */
    public static function compact(array $map): array
    {
        return array_filter($map, static fn(mixed $v): bool => $v !== null);
    }
}
