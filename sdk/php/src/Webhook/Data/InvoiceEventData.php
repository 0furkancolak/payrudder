<?php

declare(strict_types=1);

namespace Payrudder\Webhook\Data;

use Payrudder\Internal\Cast;

/**
 * The `data` of `invoice.*` events.
 * Read it with `WebhookEvent::dataAs(InvoiceEventData::class)`. Money is an int of minor units.
 */
final class InvoiceEventData implements EventData
{
    public function __construct(
        public readonly string $invoiceId,
        public readonly int $totalMinor,
        public readonly ?string $number = null,
        public readonly ?string $customerId = null,
        public readonly ?string $customerExternalId = null,
        public readonly ?string $periodId = null,
        public readonly ?string $currency = null,
        public readonly ?string $status = null,
        public readonly ?string $environment = null,
        public readonly ?int $taxMinor = null,
        public readonly ?int $netMinor = null,
    ) {}

    /**
     * @param array<string,mixed> $data
     */
    public static function fromArray(array $data): self
    {
        return new self(
            invoiceId: Cast::string($data['invoice_id'] ?? null) ?? '',
            totalMinor: Cast::int($data['total_minor'] ?? null) ?? 0,
            number: Cast::string($data['number'] ?? null),
            customerId: Cast::string($data['customer_id'] ?? null),
            customerExternalId: Cast::string($data['customer_external_id'] ?? null),
            periodId: Cast::string($data['period_id'] ?? null),
            currency: Cast::string($data['currency'] ?? null),
            status: Cast::string($data['status'] ?? null),
            environment: Cast::string($data['environment'] ?? null),
            taxMinor: Cast::int($data['tax_minor'] ?? null),
            netMinor: Cast::int($data['net_minor'] ?? null),
        );
    }
}
