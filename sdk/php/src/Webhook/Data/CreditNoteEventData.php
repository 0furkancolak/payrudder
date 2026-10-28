<?php

declare(strict_types=1);

namespace Payrudder\Webhook\Data;

use Payrudder\Internal\Cast;

/**
 * The `data` of `credit_note.*` events.
 * Read it with `WebhookEvent::dataAs(CreditNoteEventData::class)`. Money is an int of minor units.
 */
final class CreditNoteEventData implements EventData
{
    public function __construct(
        public readonly string $creditNoteId,
        public readonly int $totalMinor,
        public readonly ?string $number = null,
        public readonly ?string $invoiceId = null,
        public readonly ?string $invoiceNumber = null,
        public readonly ?string $customerId = null,
        public readonly ?string $customerExternalId = null,
        public readonly ?string $currency = null,
        public readonly ?string $reason = null,
        public readonly ?string $disposition = null,
        public readonly ?string $refundState = null,
        public readonly ?string $refundError = null,
    ) {}

    /**
     * @param array<string,mixed> $data
     */
    public static function fromArray(array $data): self
    {
        return new self(
            creditNoteId: Cast::string($data['credit_note_id'] ?? null) ?? '',
            totalMinor: Cast::int($data['total_minor'] ?? null) ?? 0,
            number: Cast::string($data['number'] ?? null),
            invoiceId: Cast::string($data['invoice_id'] ?? null),
            invoiceNumber: Cast::string($data['invoice_number'] ?? null),
            customerId: Cast::string($data['customer_id'] ?? null),
            customerExternalId: Cast::string($data['customer_external_id'] ?? null),
            currency: Cast::string($data['currency'] ?? null),
            reason: Cast::string($data['reason'] ?? null),
            disposition: Cast::string($data['disposition'] ?? null),
            refundState: Cast::string($data['refund_state'] ?? null),
            refundError: Cast::string($data['refund_error'] ?? null),
        );
    }
}
