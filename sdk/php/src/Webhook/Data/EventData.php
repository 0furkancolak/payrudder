<?php

declare(strict_types=1);

namespace Payrudder\Webhook\Data;

/**
 * The `data` of a webhook event family, read with `WebhookEvent::dataAs()`.
 */
interface EventData
{
    /**
     * @param array<string,mixed> $data
     */
    public static function fromArray(array $data): self;
}
