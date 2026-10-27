<?php

declare(strict_types=1);

namespace Payrudder\Tests;

use Http\Discovery\Psr17FactoryDiscovery;
use Nyholm\Psr7\Factory\Psr17Factory;
use Nyholm\Psr7\Response;
use Payrudder\Client;
use Payrudder\Exception\ConfigurationException;
use Payrudder\Exception\ConnectionException;
use Payrudder\Exception\NotFoundException;
use Payrudder\Tests\Support\FakeTransport as F;
use Payrudder\Tests\Support\TestCase;
use Payrudder\Transport\Psr18Transport;
use Psr\Http\Client\ClientInterface;
use Psr\Http\Client\NetworkExceptionInterface;
use Psr\Http\Message\RequestInterface;
use Psr\Http\Message\ResponseInterface;
use RuntimeException;

final class ClientTest extends TestCase
{
    public function testSendsAuthAndDefaultHeaders(): void
    {
        $this->transport->push(F::json(200, ['meters' => []]));
        $this->client()->meters->listMeters();

        $r = $this->transport->requests[0];
        self::assertSame('GET', $r->method);
        self::assertSame('https://api.example.test/v1/meters', $r->url);
        self::assertSame('Bearer qk_test_abcdefgh_secret', $r->headers['Authorization']);
        self::assertSame('payrudder-php/0.0.0', $r->headers['User-Agent']);
        self::assertSame('application/json', $r->headers['Accept']);
        self::assertArrayNotHasKey('Content-Type', $r->headers);
        self::assertSame(30.0, $r->timeout);
    }

    public function testPostSendsJson(): void
    {
        $this->transport->push(F::json(201, ['external_id' => 'c1']));
        $c = $this->client()->customers->createCustomer(['external_id' => 'c1', 'name' => 'N', 'email' => 'e@example.com']);

        self::assertSame('c1', $c->externalId);
        self::assertSame('application/json', $this->transport->requests[0]->headers['Content-Type']);
        self::assertSame('{"external_id":"c1","name":"N","email":"e@example.com"}', $this->transport->requests[0]->body);
    }

    public function testEnvironmentVariablesAreUsedWhenNothingIsPassed(): void
    {
        putenv('PAYRUDDER_API_KEY=qk_live_envkey_secret');
        putenv('PAYRUDDER_BASE_URL=https://env.example.test///');
        try {
            $t = new F();
            $t->push(F::json(200, ['meters' => []]));
            (new Client(transport: $t))->meters->listMeters();

            self::assertSame('https://env.example.test/v1/meters', $t->requests[0]->url);
            self::assertSame('Bearer qk_live_envkey_secret', $t->requests[0]->headers['Authorization']);
        } finally {
            putenv('PAYRUDDER_API_KEY');
            putenv('PAYRUDDER_BASE_URL');
        }
    }

    public function testDefaultsToTheLocalGateway(): void
    {
        putenv('PAYRUDDER_BASE_URL');
        $t = new F();
        $t->push(F::json(200, ['meters' => []]));
        (new Client(apiKey: 'k', transport: $t))->meters->listMeters();

        self::assertSame('http://localhost:42800/v1/meters', $t->requests[0]->url);
    }

    public function testMissingApiKeyIsAConfigurationError(): void
    {
        putenv('PAYRUDDER_API_KEY');
        $this->expectException(ConfigurationException::class);
        new Client(transport: new F());
    }

    public function testBadBaseUrlAndTimeoutAreRefused(): void
    {
        $attempts = [
            static fn() => new Client(apiKey: 'k', baseUrl: 'ftp://x', transport: new F()),
            static fn() => new Client(apiKey: 'k', timeout: 0.0, transport: new F()),
            static fn() => new Client(apiKey: 'k', maxRetries: -1, transport: new F()),
        ];
        foreach ($attempts as $attempt) {
            try {
                $attempt();
                self::fail('expected a configuration error');
            } catch (ConfigurationException) {
                $this->addToAssertionCount(1);
            }
        }
    }

    public function testCustomUserAgentAndTimeout(): void
    {
        $t = new F();
        $t->push(F::json(200, ['meters' => []]));
        (new Client(apiKey: 'k', timeout: 5.5, userAgent: 'my-app/1', transport: $t))->meters->listMeters();

        self::assertSame('my-app/1', $t->requests[0]->headers['User-Agent']);
        self::assertSame(5.5, $t->requests[0]->timeout);
    }

    public function testPsr18ClientWithDiscoveredFactories(): void
    {
        $captured = [];
        $psr = new class ($captured) implements ClientInterface {
            /** @param list<RequestInterface> $seen */
            public function __construct(public array &$seen) {}

            public function sendRequest(RequestInterface $request): ResponseInterface
            {
                $this->seen[] = $request;

                return new Response(404, ['Content-Type' => 'application/json', 'X-Request-Id' => 'r1'], '{"error":{"code":"NOT_FOUND","key":"common.not_found","message":"no such customer"}}');
            }
        };
        $client = new Client(apiKey: 'k', baseUrl: 'http://psr.test', httpClient: $psr, maxRetries: 0);

        try {
            $client->customers->getCustomer('zz');
            self::fail('expected an exception');
        } catch (NotFoundException $e) {
            self::assertSame('common.not_found', $e->errorKey);
        }
        self::assertSame('http://psr.test/v1/customers/zz', (string) $captured[0]->getUri());
        self::assertSame('Bearer k', $captured[0]->getHeaderLine('Authorization'));
    }

    public function testPsr18NetworkErrorBecomesConnectionException(): void
    {
        $psr = new class implements ClientInterface {
            public function sendRequest(RequestInterface $request): ResponseInterface
            {
                throw new class ('connection refused') extends RuntimeException implements NetworkExceptionInterface {
                    public function getRequest(): RequestInterface
                    {
                        return (new Psr17Factory())->createRequest('GET', 'http://x');
                    }
                };
            }
        };
        $transport = new Psr18Transport($psr, Psr17FactoryDiscovery::findRequestFactory(), Psr17FactoryDiscovery::findStreamFactory());
        $client = new Client(apiKey: 'k', transport: $transport, maxRetries: 0);

        $this->expectException(ConnectionException::class);
        $client->meters->listMeters();
    }
}
