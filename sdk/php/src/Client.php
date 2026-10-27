<?php

declare(strict_types=1);

namespace Payrudder;

use Closure;
use Payrudder\Exception\ConfigurationException;
use Payrudder\Internal\Requester;
use Payrudder\Resource\Generated\ResourceHolder;
use Payrudder\Transport\CurlTransport;
use Payrudder\Transport\Psr18Transport;
use Payrudder\Transport\TransportInterface;
use Psr\Http\Client\ClientInterface;
use Psr\Http\Message\RequestFactoryInterface;
use Psr\Http\Message\StreamFactoryInterface;

/**
 * Entry point of the SDK. One method per operation of the tenant API (`/v1/*`), grouped in
 * resource namespaces: `$client->customers->createCustomer(...)`.
 *
 * The HTTP transport is, in order: the `transport` given, a PSR-18 `httpClient` given (factories
 * are discovered when missing), ext-curl, a client found by php-http/discovery.
 */
final class Client extends ResourceHolder
{
    public const VERSION = '0.0.0';
    public const DEFAULT_BASE_URL = 'http://localhost:42800';

    /**
     * @param string|null $apiKey `qk_test_...` or `qk_live_...`; defaults to $PAYRUDDER_API_KEY
     * @param string|null $baseUrl defaults to $PAYRUDDER_BASE_URL, then http://localhost:42800
     * @param float $timeout seconds per attempt
     * @param int $maxRetries automatic retries after the first attempt (0 disables)
     * @param string|null $userAgent replaces the default `payrudder-php/<version>`
     * @param TransportInterface|null $transport replaces the whole HTTP layer (tests, custom stacks)
     * @param ClientInterface|null $httpClient any PSR-18 client
     * @param Closure(float):void|null $sleeper how to wait between retries (seconds); defaults to usleep
     */
    public function __construct(
        ?string $apiKey = null,
        ?string $baseUrl = null,
        float $timeout = 30.0,
        int $maxRetries = 2,
        ?string $userAgent = null,
        ?TransportInterface $transport = null,
        ?ClientInterface $httpClient = null,
        ?RequestFactoryInterface $requestFactory = null,
        ?StreamFactoryInterface $streamFactory = null,
        ?Closure $sleeper = null,
    ) {
        if (\PHP_INT_SIZE < 8) {
            throw new ConfigurationException('the Payrudder SDK needs a 64-bit PHP: money amounts are int64');
        }
        $apiKey = self::nonEmpty($apiKey) ?? self::nonEmpty(self::env('PAYRUDDER_API_KEY'));
        if ($apiKey === null) {
            throw new ConfigurationException('an API key is required: pass apiKey or set PAYRUDDER_API_KEY');
        }
        $baseUrl = self::nonEmpty($baseUrl) ?? self::nonEmpty(self::env('PAYRUDDER_BASE_URL')) ?? self::DEFAULT_BASE_URL;
        $baseUrl = rtrim($baseUrl, '/');
        if (preg_match('#^https?://[^/\s]+#i', $baseUrl) !== 1) {
            throw new ConfigurationException('baseUrl must start with http:// or https://');
        }
        if ($timeout <= 0) {
            throw new ConfigurationException('timeout must be above zero');
        }
        if ($maxRetries < 0) {
            throw new ConfigurationException('maxRetries must not be negative');
        }

        $transport ??= self::defaultTransport($httpClient, $requestFactory, $streamFactory);

        parent::__construct(new Requester(
            $transport,
            $baseUrl,
            $apiKey,
            $timeout,
            $maxRetries,
            $userAgent ?? 'payrudder-php/' . self::VERSION,
            $sleeper ?? static function (float $seconds): void {
                usleep((int) round($seconds * 1_000_000));
            },
        ));
    }

    private static function defaultTransport(?ClientInterface $httpClient, ?RequestFactoryInterface $requestFactory, ?StreamFactoryInterface $streamFactory): TransportInterface
    {
        if ($httpClient !== null) {
            return Psr18Transport::discover($httpClient, $requestFactory, $streamFactory);
        }
        if (\extension_loaded('curl')) {
            return new CurlTransport();
        }

        return Psr18Transport::discover();
    }

    private static function nonEmpty(?string $v): ?string
    {
        return $v === null || trim($v) === '' ? null : trim($v);
    }

    private static function env(string $name): ?string
    {
        $v = getenv($name);

        return \is_string($v) ? $v : null;
    }
}
