<?php

declare(strict_types=1);

namespace Payrudder\Webhook\Data;

use Payrudder\Internal\Cast;

/**
 * The `data` of `payment_link.*` events.
 * Read it with `WebhookEvent::dataAs(PaymentLinkEventData::class)`. Money is an int of minor units.
 */
final class PaymentLinkEventData
{
    public function __construct(
        public readonly string $linkId,
        public readonly ?string $kind = null,
        public readonly ?string $invoiceId = null,
        public readonly ?string $invoiceNumber = null,
        public readonly ?int $amountMinor = null,
        public readonly ?int $totalMinor = null,
        public readonly ?string $currency = null,
        public readonly ?string $description = null,
        public readonly ?string $customerExternalId = null,
        public readonly ?bool $singleUse = null,
        public readonly ?string $expiresAt = null,
        public readonly ?int $paidCount = null,
    ) {}

    /**
     * @param array<string,mixed> $data
     */
    public static function fromArray(array $data): self
    {
        return new self(
            linkId: Cast::string($data['link_id'] ?? null) ?? '',
            kind: Cast::string($data['kind'] ?? null),
            invoiceId: Cast::string($data['invoice_id'] ?? null),
            invoiceNumber: Cast::string($data['invoice_number'] ?? null),
            amountMinor: Cast::int($data['amount_minor'] ?? null),
            totalMinor: Cast::int($data['total_minor'] ?? null),
            currency: Cast::string($data['currency'] ?? null),
            description: Cast::string($data['description'] ?? null),
            customerExternalId: Cast::string($data['customer_external_id'] ?? null),
            singleUse: Cast::bool($data['single_use'] ?? null),
            expiresAt: Cast::string($data['expires_at'] ?? null),
            paidCount: Cast::int($data['paid_count'] ?? null),
        );
    }
}
