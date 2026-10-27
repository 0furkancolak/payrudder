<?php

declare(strict_types=1);

namespace Payrudder\Webhook;

use InvalidArgumentException;
use Payrudder\Exception\WebhookException;

/**
 * Verifies the `Payrudder-Signature` header of a webhook delivery (docs/16-webhooks.md).
 *
 * The header is `t=<unix>,v1=<hex>[,v1=<hex>]`; each `v1` is HMAC-SHA256(secret, "<t>.<raw body>")
 * in hex. There are two while a rotated secret is still accepted. Always pass the raw request
 * body, byte for byte: a re-encoded JSON document will not match.
 */
final class Webhook
{
    public const HEADER = 'Payrudder-Signature';
    public const DEFAULT_TOLERANCE = 300;

    /**
     * @param int $tolerance max distance in seconds between the signed timestamp and now
     * @param int|null $now unix time to compare with (tests); defaults to the clock
     *
     * @throws WebhookException when the signature does not match or the timestamp is stale
     */
    public static function verify(string $payload, string $header, string $secret, int $tolerance = self::DEFAULT_TOLERANCE, ?int $now = null): void
    {
        if ($tolerance < 0) {
            throw new InvalidArgumentException('tolerance must not be negative');
        }
        $timestamp = null;
        $signatures = [];
        foreach (explode(',', $header) as $part) {
            $pos = strpos($part, '=');
            if ($pos === false) {
                continue;
            }
            $name = trim(substr($part, 0, $pos));
            $value = trim(substr($part, $pos + 1));
            if ($name === 't' && preg_match('/^\d{1,18}$/', $value) === 1) {
                $timestamp = (int) $value;
            } elseif ($name === 'v1' && $value !== '') {
                $signatures[] = strtolower($value);
            }
        }
        if ($timestamp === null) {
            throw new WebhookException('the signature header has no valid timestamp');
        }
        if ($signatures === []) {
            throw new WebhookException('the signature header has no v1 signature');
        }

        $expected = hash_hmac('sha256', $timestamp . '.' . $payload, $secret);
        $match = false;
        foreach ($signatures as $candidate) {
            // no early exit: every candidate is compared, in constant time
            $match = hash_equals($expected, $candidate) || $match;
        }
        if (!$match) {
            throw new WebhookException('no signature matches the payload');
        }
        if (abs(($now ?? time()) - $timestamp) > $tolerance) {
            throw new WebhookException('the timestamp is outside the tolerance of ' . $tolerance . ' seconds');
        }
    }

    /**
     * Same as verify(), as a boolean.
     */
    public static function isValid(string $payload, string $header, string $secret, int $tolerance = self::DEFAULT_TOLERANCE, ?int $now = null): bool
    {
        try {
            self::verify($payload, $header, $secret, $tolerance, $now);
        } catch (WebhookException) {
            return false;
        }

        return true;
    }

    /**
     * Verifies the delivery and parses it into an event.
     *
     * @throws WebhookException
     */
    public static function constructEvent(string $payload, string $header, string $secret, int $tolerance = self::DEFAULT_TOLERANCE, ?int $now = null): WebhookEvent
    {
        self::verify($payload, $header, $secret, $tolerance, $now);

        return WebhookEvent::fromJson($payload);
    }
}
