<?php

declare(strict_types=1);

namespace Payrudder\Tests;

use Payrudder\Exception\ApiException;
use Payrudder\Exception\AuthenticationException;
use Payrudder\Exception\ConflictException;
use Payrudder\Exception\NotFoundException;
use Payrudder\Exception\PermissionException;
use Payrudder\Exception\RateLimitException;
use Payrudder\Exception\ServerException;
use Payrudder\Exception\ValidationException;
use Payrudder\Tests\Support\FakeTransport as F;
use Payrudder\Tests\Support\TestCase;
use Payrudder\Transport\HttpResponse;
use PHPUnit\Framework\Attributes\DataProvider;

final class ErrorsTest extends TestCase
{
    /**
     * @return iterable<string,array{int,class-string<ApiException>}>
     */
    public static function statuses(): iterable
    {
        yield '401' => [401, AuthenticationException::class];
        yield '403' => [403, PermissionException::class];
        yield '404' => [404, NotFoundException::class];
        yield '400' => [400, ValidationException::class];
        yield '422' => [422, ValidationException::class];
        yield '409' => [409, ConflictException::class];
        yield '429' => [429, RateLimitException::class];
        yield '500' => [500, ServerException::class];
        yield '413' => [413, ApiException::class];
    }

    /**
     * @param class-string<ApiException> $class
     */
    #[DataProvider('statuses')]
    public function testStatusMapsToSubtype(int $status, string $class): void
    {
        $this->transport->push(F::error($status, 'CODE_X', 'billing.credit_note.nothing_to_refund', 'nothing to refund'));
        try {
            $this->client(0)->creditNotes->getCreditNote('0192a000-0000-7000-8000-000000000000');
            self::fail('expected an exception');
        } catch (ApiException $e) {
            self::assertSame($class, $e::class);
            self::assertSame($status, $e->status);
            self::assertSame('CODE_X', $e->errorCode);
            self::assertSame('billing.credit_note.nothing_to_refund', $e->errorKey);
            self::assertSame('nothing to refund', $e->getMessage());
            self::assertSame('req_1', $e->requestId);
        }
    }

    public function testRequestIdFallsBackToTheHeaderAndBodiesThatAreNotJsonStillThrow(): void
    {
        $this->transport->push(new HttpResponse(502, ['x-request-id' => 'hdr-7'], '<html>bad gateway</html>'));
        try {
            $this->client(0)->meters->listMeters();
            self::fail('expected an exception');
        } catch (ServerException $e) {
            self::assertSame('hdr-7', $e->requestId);
            self::assertSame('HTTP 502', $e->getMessage());
            self::assertNull($e->errorKey);
            self::assertSame('<html>bad gateway</html>', $e->responseBody);
        }
    }

    public function testRetryAfterAsHttpDate(): void
    {
        self::assertSame(30.0, ApiException::parseRetryAfter('Wed, 21 Oct 2015 07:28:30 GMT', (int) strtotime('Wed, 21 Oct 2015 07:28:00 GMT')));
        self::assertNull(ApiException::parseRetryAfter('soon'));
    }
}
