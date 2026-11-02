<?php

declare(strict_types=1);

namespace Payrudder\Webhook\Data;

use Payrudder\Internal\Cast;

/**
 * The `data` of `price_change.scheduled`: a run of a price change scheduled at least one subscription. `scheduled` and `skipped` are what that run did.
 * `priceChange` is the price change as the API returns it, without the fields at their zero value (`id`, `plan_code`, `from_version`, `to_version`, `notice_days`,
 * `effective_time`, `state`, `create_time` and, when set, `effective_from`, `created_by`, `withdraw_time`, `withdrawn_by` and the counts `scheduled`, `applied`, `withdrawn`, `skipped`).
 * Read it with `WebhookEvent::dataAs(PriceChangeScheduledEventData::class)`.
 */
final class PriceChangeScheduledEventData implements EventData
{
    /**
     * @param array<string,mixed> $priceChange
     */
    public function __construct(
        public readonly array $priceChange,
        public readonly int $scheduled,
        public readonly int $skipped,
    ) {}

    /**
     * @param array<string,mixed> $data
     */
    public static function fromArray(array $data): self
    {
        return new self(
            priceChange: Cast::map($data['price_change'] ?? null) ?? [],
            scheduled: Cast::int($data['scheduled'] ?? null) ?? 0,
            skipped: Cast::int($data['skipped'] ?? null) ?? 0,
        );
    }
}
