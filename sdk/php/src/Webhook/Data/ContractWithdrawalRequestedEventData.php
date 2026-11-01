<?php

declare(strict_types=1);

namespace Payrudder\Webhook\Data;

use Payrudder\Internal\Cast;

/**
 * The `data` of `contract.withdrawal_requested`.
 * Read it with `WebhookEvent::dataAs(ContractWithdrawalRequestedEventData::class)`.
 */
final class ContractWithdrawalRequestedEventData implements EventData
{
    public function __construct(
        public readonly string $withdrawalId,
        public readonly string $customerId,
        public readonly string $customerExternalId,
        public readonly string $source,
        public readonly string $receivedAt,
        public readonly ?string $subscriptionId = null,
        public readonly ?string $invoiceId = null,
    ) {}

    /**
     * @param array<string,mixed> $data
     */
    public static function fromArray(array $data): self
    {
        return new self(
            withdrawalId: Cast::string($data['withdrawal_id'] ?? null) ?? '',
            customerId: Cast::string($data['customer_id'] ?? null) ?? '',
            customerExternalId: Cast::string($data['customer_external_id'] ?? null) ?? '',
            source: Cast::string($data['source'] ?? null) ?? '',
            receivedAt: Cast::string($data['received_at'] ?? null) ?? '',
            subscriptionId: Cast::string($data['subscription_id'] ?? null),
            invoiceId: Cast::string($data['invoice_id'] ?? null),
        );
    }
}
