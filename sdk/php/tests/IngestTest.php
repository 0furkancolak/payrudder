<?php

declare(strict_types=1);

namespace Payrudder\Tests;

use BackedEnum;
use InvalidArgumentException;
use Payrudder\Enum\EventResultStatus;
use Payrudder\Exception\RateLimitException;
use Payrudder\Exception\UnexpectedResponseException;
use Payrudder\Model\IncomingEvent;
use Payrudder\Tests\Support\FakeTransport as F;
use Payrudder\Tests\Support\TestCase;
use Payrudder\Transport\HttpRequest;
use Payrudder\Transport\HttpResponse;

final class IngestTest extends TestCase
{
    /**
     * @return array<string,mixed>
     */
    private static function event(int $i, string $pad = ''): array
    {
        return [
            'customer_external_id' => 'cus_' . $i,
            'meter_code' => 'ai_tokens',
            'quantity' => '1.25',
            'event_time' => '2026-11-03T10:15:00Z',
            'properties' => $pad === '' ? [] : ['pad' => $pad],
        ];
    }

    /**
     * An echo server: accepts everything, in order.
     */
    private function acceptAll(): void
    {
        $this->transport->handle(static function (HttpRequest $r): HttpResponse {
            $results = [];
            $events = F::decode($r->body ?? '')['events'] ?? [];
            foreach (\is_array($events) ? $events : [] as $e) {
                $id = \is_array($e) ? ($e['customer_external_id'] ?? '') : '';
                $results[] = ['status' => 'accepted', 'event_id' => 'id-' . (\is_string($id) ? $id : '')];
            }

            return F::json(202, ['results' => $results]);
        });
    }

    public function testSplitsByCountAndKeepsOrder(): void
    {
        $this->acceptAll();
        $events = [];
        for ($i = 0; $i < 1203; ++$i) {
            $events[] = self::event($i);
        }
        $results = $this->client()->events->ingest($events);

        self::assertCount(3, $this->transport->requests);
        self::assertCount(500, $this->transport->events(0));
        self::assertCount(500, $this->transport->events(1));
        self::assertCount(203, $this->transport->events(2));
        self::assertCount(1203, $results);
        self::assertSame('id-cus_0', $results[0]->eventId);
        self::assertSame('id-cus_1202', $results[1202]->eventId);
        self::assertSame(EventResultStatus::Accepted, $results[5]->status);
    }

    public function testSplitsBySize(): void
    {
        $this->acceptAll();
        $pad = str_repeat('x', 300_000);
        $events = [self::event(0, $pad), self::event(1, $pad), self::event(2, $pad), self::event(3, $pad), self::event(4, $pad)];
        $this->client()->events->ingest($events);

        self::assertCount(2, $this->transport->requests); // 3 x 300 kB fit under 1 MiB, the next two start a new request
        foreach ($this->transport->requests as $r) {
            self::assertLessThanOrEqual(1_048_576, \strlen($r->body ?? ''));
        }
    }

    public function testAnEventThatCannotFitIsRefusedBeforeAnythingIsSent(): void
    {
        $this->acceptAll();
        $this->expectException(InvalidArgumentException::class);
        try {
            $this->client()->events->ingest([self::event(0), self::event(1, str_repeat('x', 1_100_000))]);
        } finally {
            self::assertCount(0, $this->transport->requests);
        }
    }

    public function testFillsIdempotencyKeysAndKeepsGivenOnes(): void
    {
        $this->acceptAll();
        $given = self::event(1) + ['idempotency_key' => 'mine-1'];
        $this->client()->events->ingest([self::event(0), $given, new IncomingEvent('c', 'm', '3', '2026-11-03T10:15:00Z')]);

        $keys = $this->transport->eventKeys();
        self::assertMatchesRegularExpression('/^evt_[0-9a-f]{32}$/', $keys[0]);
        self::assertSame('mine-1', $keys[1]);
        self::assertMatchesRegularExpression('/^evt_[0-9a-f]{32}$/', $keys[2]);
        self::assertNotSame($keys[0], $keys[2]);
    }

    public function testKeysAreStableAcrossRetriesEvenAfterAServerError(): void
    {
        $ok = F::json(202, ['results' => [['status' => 'accepted', 'event_id' => 'e1']]]);
        $this->transport->push(F::error(500, 'INTERNAL', 'common.internal'), F::error(429, 'RESOURCE_EXHAUSTED', 'common.rate_limited', headers: ['Retry-After' => '1']), $ok);
        $this->client()->events->ingest([self::event(0)]);

        self::assertCount(3, $this->transport->requests); // a 500 is retried because the call carries idempotency keys
        $first = $this->transport->requests[0]->body;
        self::assertSame($first, $this->transport->requests[1]->body);
        self::assertSame($first, $this->transport->requests[2]->body);
    }

    public function testRejectedAndDuplicateEventsDoNotThrow(): void
    {
        $this->transport->push(F::json(202, ['results' => [
            ['status' => 'accepted', 'event_id' => 'e1'],
            ['status' => 'duplicate', 'event_id' => 'e0'],
            ['status' => 'rejected', 'error_code' => 'UNKNOWN_METER', 'error_message' => 'no such meter'],
        ]]));
        $r = $this->client()->events->ingest([self::event(0), self::event(1), self::event(2)]);

        self::assertSame(EventResultStatus::Duplicate, $r[1]->status);
        self::assertSame(EventResultStatus::Rejected, $r[2]->status);
        self::assertSame('UNKNOWN_METER', $r[2]->errorCode instanceof BackedEnum ? $r[2]->errorCode->value : $r[2]->errorCode);
    }

    public function testNothingToSendMakesNoRequest(): void
    {
        self::assertSame([], $this->client()->events->ingest([]));
        self::assertCount(0, $this->transport->requests);
    }

    public function testAWholeBatchErrorThrows(): void
    {
        $this->transport->push(F::error(429, 'RESOURCE_EXHAUSTED', 'gateway.rate_limited'));
        $this->expectException(RateLimitException::class);
        $this->client(0)->events->ingest([self::event(0)]);
    }

    public function testResultCountMismatchIsAnError(): void
    {
        $this->transport->push(F::json(202, ['results' => []]));
        $this->expectException(UnexpectedResponseException::class);
        $this->client()->events->ingest([self::event(0)]);
    }

    public function testQuantityStaysAStringOnTheWire(): void
    {
        $this->acceptAll();
        $this->client()->events->ingest([new IncomingEvent('c', 'm', '1.250', '2026-11-03T10:15:00Z', properties: ['region' => 'eu'])]);

        self::assertStringContainsString('"quantity":"1.250"', $this->transport->requests[0]->body ?? '');
    }
}
