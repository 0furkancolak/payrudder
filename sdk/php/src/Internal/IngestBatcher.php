<?php

declare(strict_types=1);

namespace Payrudder\Internal;

use InvalidArgumentException;
use Payrudder\Contract\RequestBody;

/**
 * Prepares events for POST /v1/events: fills idempotency keys and cuts valid batches.
 *
 * @internal
 */
final class IngestBatcher
{
    public const MAX_EVENTS = 500;
    public const MAX_BYTES = 1_048_576;
    private const ENVELOPE_BYTES = 13; // {"events":[]}

    /**
     * @param iterable<RequestBody|array<string,mixed>> $events
     *
     * @return list<list<array<string,mixed>>> batches of events, each valid for one request
     *
     * @throws InvalidArgumentException when one event alone is larger than a request may be
     */
    public static function batches(iterable $events): array
    {
        $batches = [];
        $current = [];
        $size = self::ENVELOPE_BYTES;
        foreach ($events as $event) {
            $map = Cast::body($event);
            $key = $map['idempotency_key'] ?? null;
            if (!\is_string($key) || $key === '') {
                $map['idempotency_key'] = 'evt_' . bin2hex(random_bytes(16));
            }
            $len = \strlen(json_encode($map, JSON_THROW_ON_ERROR | JSON_UNESCAPED_SLASHES | JSON_UNESCAPED_UNICODE | JSON_PRESERVE_ZERO_FRACTION));
            if ($len + self::ENVELOPE_BYTES > self::MAX_BYTES) {
                throw new InvalidArgumentException('a single event is larger than the 1 MiB request limit');
            }
            $add = $len + ($current === [] ? 0 : 1);
            if ($current !== [] && (\count($current) >= self::MAX_EVENTS || $size + $add > self::MAX_BYTES)) {
                $batches[] = $current;
                $current = [];
                $size = self::ENVELOPE_BYTES;
                $add = $len;
            }
            $current[] = $map;
            $size += $add;
        }
        if ($current !== []) {
            $batches[] = $current;
        }

        return $batches;
    }
}
