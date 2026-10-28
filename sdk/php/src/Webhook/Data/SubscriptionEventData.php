<?php

declare(strict_types=1);

namespace Payrudder\Webhook\Data;

use Payrudder\Internal\Cast;
use Payrudder\Model\ChangeQuote;
use Payrudder\Model\Subscription;

/**
 * The `data` of `subscription.*` events.
 * Read it with `WebhookEvent::dataAs(SubscriptionEventData::class)`. Money is an int of minor units.
 */
final class SubscriptionEventData
{
    public function __construct(
        public readonly Subscription $subscription,
        public readonly ?string $previousSubscriptionId = null,
        public readonly ?ChangeQuote $quote = null,
        public readonly ?string $toProvider = null,
        public readonly ?int $creditedMinor = null,
    ) {}

    /**
     * @param array<string,mixed> $data
     */
    public static function fromArray(array $data): self
    {
        return new self(
            subscription: Subscription::fromArray(\is_array($data['subscription'] ?? null) ? $data['subscription'] : []),
            previousSubscriptionId: Cast::string($data['previous_subscription_id'] ?? null),
            quote: \is_array($data['quote'] ?? null) ? ChangeQuote::fromArray($data['quote']) : null,
            toProvider: Cast::string($data['to_provider'] ?? null),
            creditedMinor: Cast::int($data['credited_minor'] ?? null),
        );
    }
}
