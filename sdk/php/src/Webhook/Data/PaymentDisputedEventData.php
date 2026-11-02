<?php

declare(strict_types=1);

namespace Payrudder\Webhook\Data;

use Payrudder\Internal\Cast;

/**
 * The `data` of `payment.disputed`: one step of a case a customer opened with their bank about a payment (a case that opens and is later won or lost is two or three
 * events, each once). Read it with `WebhookEvent::dataAs(PaymentDisputedEventData::class)`.
 */
final class PaymentDisputedEventData implements EventData
{
    /**
     * @param string $disputeId the provider's reference of the case, the same in every event about it
     * @param string $amountMinor a decimal string in the smallest unit of the currency
     * @param string $outcome where the case stands: open, won or lost
     * @param bool $formal true for a chargeback, which takes the money back; false for an inquiry or a warning ahead of one
     * @param string $reason the cardholder's reason in the provider's words; may be empty
     * @param ?string $evidenceDueTime an RFC 3339 time, set when the provider gives a date
     */
    public function __construct(
        public readonly string $invoiceId,
        public readonly string $invoiceNumber,
        public readonly string $customerExternalId,
        public readonly string $provider,
        public readonly string $disputeId,
        public readonly string $paymentRef,
        public readonly string $amountMinor,
        public readonly string $currency,
        public readonly string $outcome,
        public readonly bool $formal,
        public readonly string $reason,
        public readonly string $providerStatus,
        public readonly ?string $evidenceDueTime = null,
    ) {}

    /**
     * @param array<string,mixed> $data
     */
    public static function fromArray(array $data): self
    {
        return new self(
            invoiceId: Cast::string($data['invoice_id'] ?? null) ?? '',
            invoiceNumber: Cast::string($data['invoice_number'] ?? null) ?? '',
            customerExternalId: Cast::string($data['customer_external_id'] ?? null) ?? '',
            provider: Cast::string($data['provider'] ?? null) ?? '',
            disputeId: Cast::string($data['dispute_id'] ?? null) ?? '',
            paymentRef: Cast::string($data['payment_ref'] ?? null) ?? '',
            amountMinor: Cast::string($data['amount_minor'] ?? null) ?? '0',
            currency: Cast::string($data['currency'] ?? null) ?? '',
            outcome: Cast::string($data['outcome'] ?? null) ?? '',
            formal: Cast::bool($data['formal'] ?? null) ?? false,
            reason: Cast::string($data['reason'] ?? null) ?? '',
            providerStatus: Cast::string($data['provider_status'] ?? null) ?? '',
            evidenceDueTime: Cast::string($data['evidence_due_time'] ?? null),
        );
    }
}
