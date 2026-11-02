<?php

declare(strict_types=1);

namespace Payrudder\Webhook\Data;

use Payrudder\Internal\Cast;

/**
 * The `data` of `contract.withdrawal_decided`.
 * Read it with `WebhookEvent::dataAs(ContractWithdrawalDecidedEventData::class)`.
 */
final class ContractWithdrawalDecidedEventData implements EventData
{
    public function __construct(
        public readonly string $withdrawalId,
        public readonly string $customerId,
        public readonly string $customerExternalId,
        public readonly string $status,
        public readonly string $decidedBy,
        public readonly string $decidedAt,
        public readonly string $note,
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
            status: Cast::string($data['status'] ?? null) ?? '',
            decidedBy: Cast::string($data['decided_by'] ?? null) ?? '',
            decidedAt: Cast::string($data['decided_at'] ?? null) ?? '',
            note: Cast::string($data['note'] ?? null) ?? '',
            subscriptionId: Cast::string($data['subscription_id'] ?? null),
            invoiceId: Cast::string($data['invoice_id'] ?? null),
        );
    }
}
