<?php

declare(strict_types=1);

namespace Payrudder\Webhook\Data;

use Payrudder\Internal\Cast;

/**
 * The `data` of `customer.blocked` and `customer.unblocked`. `category`, `reason`, `blockedUntil` and `suspendService` are set on `customer.blocked` only.
 * Read it with `WebhookEvent::dataAs(CustomerBlockEventData::class)`.
 */
final class CustomerBlockEventData implements EventData
{
    /**
     * @param ?string $category fraud, chargeback, abuse, request, other, or empty
     * @param ?string $reason the seller's own note
     * @param ?string $blockedUntil an RFC 3339 time; absent when the block lasts until it is lifted
     * @param ?bool $suspendService the block also suspended the customer's service (entitlement checks answer not allowed); absent: false
     */
    public function __construct(
        public readonly string $customerId,
        public readonly ?string $customerExternalId = null,
        public readonly ?string $category = null,
        public readonly ?string $reason = null,
        public readonly ?string $blockedUntil = null,
        public readonly ?bool $suspendService = null,
    ) {}

    /**
     * @param array<string,mixed> $data
     */
    public static function fromArray(array $data): self
    {
        return new self(
            customerId: Cast::string($data['customer_id'] ?? null) ?? '',
            customerExternalId: Cast::string($data['customer_external_id'] ?? null),
            category: Cast::string($data['category'] ?? null),
            reason: Cast::string($data['reason'] ?? null),
            blockedUntil: Cast::string($data['blocked_until'] ?? null),
            suspendService: Cast::bool($data['suspend_service'] ?? null),
        );
    }
}
