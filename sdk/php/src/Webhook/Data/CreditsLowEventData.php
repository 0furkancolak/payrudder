<?php

declare(strict_types=1);

namespace Payrudder\Webhook\Data;

use Payrudder\Internal\Cast;

/**
 * The `data` of `credits.low`. Credit amounts are decimal strings.
 * Read it with `WebhookEvent::dataAs(CreditsLowEventData::class)`. Money is an int of minor units.
 */
final class CreditsLowEventData implements EventData
{
    public function __construct(
        public readonly string $customerId,
        public readonly string $balance,
        public readonly string $threshold,
        public readonly ?string $customerExternalId = null,
    ) {}

    /**
     * @param array<string,mixed> $data
     */
    public static function fromArray(array $data): self
    {
        return new self(
            customerId: Cast::string($data['customer_id'] ?? null) ?? '',
            balance: Cast::string($data['balance'] ?? null) ?? '',
            threshold: Cast::string($data['threshold'] ?? null) ?? '',
            customerExternalId: Cast::string($data['customer_external_id'] ?? null),
        );
    }
}
