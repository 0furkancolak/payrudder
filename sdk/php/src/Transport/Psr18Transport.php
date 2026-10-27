<?php

declare(strict_types=1);

namespace Payrudder\Transport;

use Http\Discovery\Psr17FactoryDiscovery;
use Http\Discovery\Psr18ClientDiscovery;
use Payrudder\Exception\ConfigurationException;
use Payrudder\Exception\ConnectionException;
use Payrudder\Exception\TimeoutException;
use Psr\Http\Client\ClientExceptionInterface;
use Psr\Http\Client\ClientInterface;
use Psr\Http\Message\RequestFactoryInterface;
use Psr\Http\Message\StreamFactoryInterface;
use Throwable;

/**
 * Sends through any PSR-18 client. The per-request timeout is the client's own setting: PSR-18
 * has no way to pass one per call.
 */
final class Psr18Transport implements TransportInterface
{
    public function __construct(
        private readonly ClientInterface $client,
        private readonly RequestFactoryInterface $requestFactory,
        private readonly StreamFactoryInterface $streamFactory,
    ) {}

    /**
     * Finds a PSR-18 client and PSR-17 factories with php-http/discovery. Whatever is passed in is kept.
     */
    public static function discover(
        ?ClientInterface $client = null,
        ?RequestFactoryInterface $requestFactory = null,
        ?StreamFactoryInterface $streamFactory = null,
    ): self {
        if (!class_exists(Psr18ClientDiscovery::class)) {
            if ($client === null || $requestFactory === null || $streamFactory === null) {
                throw new ConfigurationException('install php-http/discovery, or pass a PSR-18 client with PSR-17 request and stream factories');
            }

            return new self($client, $requestFactory, $streamFactory);
        }

        try {
            return new self(
                $client ?? Psr18ClientDiscovery::find(),
                $requestFactory ?? Psr17FactoryDiscovery::findRequestFactory(),
                $streamFactory ?? Psr17FactoryDiscovery::findStreamFactory(),
            );
        } catch (Throwable $e) {
            throw new ConfigurationException('no PSR-18 client or PSR-17 factories found: ' . $e->getMessage(), 0, $e);
        }
    }

    public function send(HttpRequest $request): HttpResponse
    {
        $psr = $this->requestFactory->createRequest($request->method, $request->url);
        foreach ($request->headers as $name => $value) {
            $psr = $psr->withHeader($name, $value);
        }
        if ($request->body !== null) {
            $psr = $psr->withBody($this->streamFactory->createStream($request->body));
        }

        try {
            $response = $this->client->sendRequest($psr);
        } catch (ClientExceptionInterface $e) {
            $message = 'request to ' . $request->url . ' failed: ' . $e->getMessage();
            if (stripos($e->getMessage(), 'timed out') !== false) {
                throw new TimeoutException($message, 0, $e);
            }
            throw new ConnectionException($message, 0, $e);
        }

        $headers = [];
        foreach ($response->getHeaders() as $name => $values) {
            $headers[strtolower((string) $name)] = implode(', ', $values);
        }

        return new HttpResponse($response->getStatusCode(), $headers, (string) $response->getBody());
    }
}
