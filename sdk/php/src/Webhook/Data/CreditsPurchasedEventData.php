<?php

declare(strict_types=1);

namespace Payrudder\Webhook\Data;

use Payrudder\Internal\Cast;

/**
 * The `data` of `credits.purchased`. Credit amounts are decimal strings.
 * Read it with `WebhookEvent::dataAs(CreditsPurchasedEventData::class)`. Money is an int of minor units.
 */
final class CreditsPurchasedEventData
{
    public function __construct(
        public readonly string $customerId,
        public readonly string $credits,
        public readonly string $balance,
        public readonly ?string $customerExternalId = null,
        public readonly ?string $bonusCredits = null,
        public readonly ?int $quantity = null,
        public readonly ?string $packId = null,
        public readonly ?string $packName = null,
        public readonly ?string $linkId = null,
        public readonly ?string $invoiceId = null,
        public readonly ?string $invoiceNumber = null,
        public readonly ?string $currency = null,
        public readonly ?int $amountMinor = null,
        public readonly ?string $expiresAt = null,
    ) {}

    /**
     * @param array<string,mixed> $data
     */
    public static function fromArray(array $data): self
    {
        return new self(
            customerId: Cast::string($data['customer_id'] ?? null) ?? '',
            credits: Cast::string($data['credits'] ?? null) ?? '',
            balance: Cast::string($data['balance'] ?? null) ?? '',
            customerExternalId: Cast::string($data['customer_external_id'] ?? null),
            bonusCredits: Cast::string($data['bonus_credits'] ?? null),
            quantity: Cast::int($data['quantity'] ?? null),
            packId: Cast::string($data['pack_id'] ?? null),
            packName: Cast::string($data['pack_name'] ?? null),
            linkId: Cast::string($data['link_id'] ?? null),
            invoiceId: Cast::string($data['invoice_id'] ?? null),
            invoiceNumber: Cast::string($data['invoice_number'] ?? null),
            currency: Cast::string($data['currency'] ?? null),
            amountMinor: Cast::int($data['amount_minor'] ?? null),
            expiresAt: Cast::string($data['expires_at'] ?? null),
        );
    }
}
