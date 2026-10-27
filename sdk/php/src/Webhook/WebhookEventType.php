<?php

declare(strict_types=1);

namespace Payrudder\Webhook;

/**
 * The event types a delivery can carry (docs/16-webhooks.md). `ping` is sent by the test button of an endpoint.
 */
final class WebhookEventType
{
    public const PING = 'ping';
    public const INVOICE_FINALIZED = 'invoice.finalized';
    public const INVOICE_PAID = 'invoice.paid';
    public const INVOICE_VOIDED = 'invoice.voided';
    public const INVOICE_UNCOLLECTIBLE = 'invoice.uncollectible';
    public const CREDIT_NOTE_CREATED = 'credit_note.created';
    public const CREDIT_NOTE_REFUNDED = 'credit_note.refunded';
    public const CREDIT_NOTE_REFUND_FAILED = 'credit_note.refund_failed';
    public const SUBSCRIPTION_CREATED = 'subscription.created';
    public const SUBSCRIPTION_CHANGED = 'subscription.changed';
    public const SUBSCRIPTION_CHANGE_SCHEDULED = 'subscription.change_scheduled';
    public const SUBSCRIPTION_CANCELLED = 'subscription.cancelled';
    public const SUBSCRIPTION_CANCELLATION_SCHEDULED = 'subscription.cancellation_scheduled';
    public const SUBSCRIPTION_CANCELLATION_WITHDRAWN = 'subscription.cancellation_withdrawn';
    public const SUBSCRIPTION_PAUSED = 'subscription.paused';
    public const SUBSCRIPTION_RESUMED = 'subscription.resumed';
    public const SUBSCRIPTION_MOVED_TO_PROVIDER = 'subscription.moved_to_provider';
    public const SUBSCRIPTION_TRIAL_WILL_END = 'subscription.trial_will_end';
    public const PAYMENT_LINK_CREATED = 'payment_link.created';
    public const PAYMENT_LINK_PAID = 'payment_link.paid';
    public const PAYMENT_LINK_EXPIRED = 'payment_link.expired';
    public const CREDITS_PURCHASED = 'credits.purchased';
    public const CREDITS_LOW = 'credits.low';

    /**
     * Every type an endpoint can subscribe to (all but `ping`).
     *
     * @return list<string>
     */
    public static function all(): array
    {
        return [
            self::INVOICE_FINALIZED,
            self::INVOICE_PAID,
            self::INVOICE_VOIDED,
            self::INVOICE_UNCOLLECTIBLE,
            self::CREDIT_NOTE_CREATED,
            self::CREDIT_NOTE_REFUNDED,
            self::CREDIT_NOTE_REFUND_FAILED,
            self::SUBSCRIPTION_CREATED,
            self::SUBSCRIPTION_CHANGED,
            self::SUBSCRIPTION_CHANGE_SCHEDULED,
            self::SUBSCRIPTION_CANCELLED,
            self::SUBSCRIPTION_CANCELLATION_SCHEDULED,
            self::SUBSCRIPTION_CANCELLATION_WITHDRAWN,
            self::SUBSCRIPTION_PAUSED,
            self::SUBSCRIPTION_RESUMED,
            self::SUBSCRIPTION_MOVED_TO_PROVIDER,
            self::SUBSCRIPTION_TRIAL_WILL_END,
            self::PAYMENT_LINK_CREATED,
            self::PAYMENT_LINK_PAID,
            self::PAYMENT_LINK_EXPIRED,
            self::CREDITS_PURCHASED,
            self::CREDITS_LOW,
        ];
    }
}
