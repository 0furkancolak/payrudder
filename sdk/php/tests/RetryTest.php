<?php

declare(strict_types=1);

namespace Payrudder\Tests;

use Payrudder\Exception\ApiException;
use Payrudder\Exception\ConnectionException;
use Payrudder\Exception\RateLimitException;
use Payrudder\Exception\ServerException;
use Payrudder\Exception\ValidationException;
use Payrudder\RequestOptions;
use Payrudder\Tests\Support\FakeTransport as F;
use Payrudder\Tests\Support\TestCase;

final class RetryTest extends TestCase
{
    public function testRateLimitHonoursRetryAfter(): void
    {
        $this->transport->push(F::error(429, 'RESOURCE_EXHAUSTED', 'common.rate_limited', headers: ['Retry-After' => '3']), F::json(200, ['customers' => []]));
        $page = $this->client()->customers->listCustomers();

        self::assertSame([], $page->customers);
        self::assertSame([3.0], $this->sleeps);
        self::assertCount(2, $this->transport->requests);
    }

    public function testGatewayErrorsAreRetriedWithBackoff(): void
    {
        $this->transport->push(F::error(502, 'UNAVAILABLE', 'common.unavailable'), F::error(503, 'UNAVAILABLE', 'common.unavailable'), F::json(200, ['meters' => []]));
        $this->client()->meters->listMeters();

        self::assertCount(2, $this->sleeps);
        // 0.5 s and 1 s, each scaled by 50..100 % jitter
        self::assertGreaterThanOrEqual(0.25, $this->sleeps[0]);
        self::assertLessThanOrEqual(0.5, $this->sleeps[0]);
        self::assertGreaterThanOrEqual(0.5, $this->sleeps[1]);
        self::assertLessThanOrEqual(1.0, $this->sleeps[1]);
    }

    public function testGivesUpAfterMaxRetries(): void
    {
        $this->transport->push(F::error(503, 'UNAVAILABLE', 'a'), F::error(503, 'UNAVAILABLE', 'b'), F::error(503, 'UNAVAILABLE', 'c'));
        try {
            $this->client(2)->meters->listMeters();
            self::fail('expected an exception');
        } catch (ServerException $e) {
            self::assertSame('c', $e->errorKey);
        }
        self::assertCount(3, $this->transport->requests);
    }

    public function testNoRetriesWhenDisabled(): void
    {
        $this->transport->push(F::error(503, 'UNAVAILABLE', 'a'));
        $this->expectException(ServerException::class);
        $this->client(0)->meters->listMeters();
    }

    public function testNetworkErrorsAreRetried(): void
    {
        $this->transport->push(new ConnectionException('reset'), F::json(200, ['meters' => []]));
        $this->client()->meters->listMeters();

        self::assertCount(2, $this->transport->requests);
        self::assertCount(1, $this->sleeps);
    }

    public function testNetworkErrorSurfacesAfterTheRetries(): void
    {
        $this->transport->push(new ConnectionException('a'), new ConnectionException('b'), new ConnectionException('c'));
        $this->expectException(ConnectionException::class);
        $this->expectExceptionMessage('c');
        $this->client()->meters->listMeters();
    }

    public function testClientErrorsAreNeverRetried(): void
    {
        foreach ([400, 401, 403, 404, 409, 422] as $status) {
            $this->transport = new F();
            $this->transport->push(F::error($status, 'X', 'x.y'));
            try {
                $this->client()->meters->listMeters();
                self::fail("status $status should throw");
            } catch (ApiException) {
                self::assertCount(1, $this->transport->requests, "status $status");
            }
        }
    }

    public function testServerErrorIsRetriedForGetButNotForPlainPost(): void
    {
        $this->transport->push(F::error(500, 'INTERNAL', 'common.internal'), F::json(200, ['meters' => []]));
        $this->client()->meters->listMeters();
        self::assertCount(2, $this->transport->requests);

        $this->transport = new F();
        $this->transport->push(F::error(500, 'INTERNAL', 'common.internal'), F::json(201, ['code' => 'x']));
        try {
            $this->client()->meters->createMeter(['code' => 'x', 'aggregation' => 'AGGREGATION_SUM', 'unit_label' => 'u']);
            self::fail('expected an exception');
        } catch (ServerException) {
            self::assertCount(1, $this->transport->requests);
        }
    }

    public function testServerErrorIsRetriedForPostWithIdempotencyKeyAndKeyIsStable(): void
    {
        $this->transport->push(F::error(500, 'INTERNAL', 'common.internal'), F::json(201, ['code' => 'x']));
        $this->client()->meters->createMeter(['code' => 'x', 'aggregation' => 'AGGREGATION_SUM', 'unit_label' => 'u'], new RequestOptions(idempotencyKey: 'key-1'));

        self::assertCount(2, $this->transport->requests);
        self::assertSame('key-1', $this->transport->requests[0]->headers['Idempotency-Key']);
        self::assertSame('key-1', $this->transport->requests[1]->headers['Idempotency-Key']);
    }

    public function testRetryAfterBeyondTheCapThrowsAtOnce(): void
    {
        $this->transport->push(F::error(429, 'RESOURCE_EXHAUSTED', 'common.rate_limited', headers: ['Retry-After' => '3600']));
        try {
            $this->client()->meters->listMeters();
            self::fail('expected an exception');
        } catch (RateLimitException $e) {
            self::assertSame(3600.0, $e->retryAfter);
        }
        self::assertSame([], $this->sleeps);
    }

    public function testOptionsOverrideRetriesAndAddHeaders(): void
    {
        $this->transport->push(F::error(503, 'UNAVAILABLE', 'a'));
        try {
            $this->client()->meters->listMeters(new RequestOptions(maxRetries: 0, headers: ['X-Trace' => 't1']));
            self::fail('expected an exception');
        } catch (ServerException) {
        }
        self::assertCount(1, $this->transport->requests);
        self::assertSame('t1', $this->transport->requests[0]->headers['X-Trace']);
    }

    public function testValidationErrorCarriesDetails(): void
    {
        $this->transport->push(F::json(400, ['error' => ['code' => 'INVALID_ARGUMENT', 'key' => 'common.invalid', 'message' => 'bad', 'details' => [['field' => 'name', 'description' => 'required']], 'request_id' => 'r9']]));
        try {
            $this->client()->customers->getCustomer('c1');
            self::fail('expected an exception');
        } catch (ValidationException $e) {
            self::assertSame([['field' => 'name', 'description' => 'required']], $e->details);
            self::assertSame('r9', $e->requestId);
        }
    }
}
