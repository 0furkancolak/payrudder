<?php

declare(strict_types=1);

namespace Payrudder\Webhook;

use JsonException;
use Payrudder\Exception\WebhookException;

/**
 * The envelope of a delivery. `data` depends on `type` (see docs/16-webhooks.md); money in it
 * is a string of minor units, as in the REST API. Deliveries are at least once and not in order:
 * use `id` to drop a repeat.
 */
final class WebhookEvent
{
    /**
     * @param array<string,mixed> $data
     */
    public function __construct(
        public readonly string $id,
        public readonly string $type,
        public readonly string $created,
        public readonly string $tenantId,
        public readonly array $data,
    ) {}

    /**
     * The data as the class of its event family: InvoiceEventData for `invoice.*`, CreditNoteEventData for
     * `credit_note.*`, SubscriptionEventData for `subscription.*`, PaymentLinkEventData for `payment_link.*`,
     * CreditsPurchasedEventData for `credits.purchased` and CreditsLowEventData for `credits.low`.
     *
     * @template T of object
     *
     * @param class-string<T> $class one of the classes in Payrudder\Webhook\Data
     *
     * @return T
     */
    public function dataAs(string $class): object
    {
        /** @var T */
        return $class::fromArray($this->data);
    }

    /**
     * @throws WebhookException
     */
    public static function fromJson(string $json): self
    {
        try {
            $d = json_decode($json, true, 512, JSON_BIGINT_AS_STRING | JSON_THROW_ON_ERROR);
        } catch (JsonException $e) {
            throw new WebhookException('the webhook body is not valid JSON: ' . $e->getMessage(), 0, $e);
        }
        if (!\is_array($d) || !isset($d['id'], $d['type']) || !\is_string($d['id']) || !\is_string($d['type'])) {
            throw new WebhookException('the webhook body is not an event envelope');
        }
        $data = $d['data'] ?? [];
        if (!\is_array($data)) {
            throw new WebhookException('the webhook data is not an object');
        }
        /** @var array<string,mixed> $data */
        return new self(
            $d['id'],
            $d['type'],
            isset($d['created']) && \is_string($d['created']) ? $d['created'] : '',
            isset($d['tenant_id']) && \is_string($d['tenant_id']) ? $d['tenant_id'] : '',
            $data,
        );
    }
}
