<?php

declare(strict_types=1);

namespace Payrudder\Tests;

use Payrudder\Tests\Support\FakeTransport as F;
use Payrudder\Tests\Support\TestCase;

final class PaginationTest extends TestCase
{
    private function pages(): void
    {
        $this->transport->push(
            F::json(200, ['customers' => [['external_id' => 'a'], ['external_id' => 'b']], 'next_page_token' => 'T1']),
            F::json(200, ['customers' => [['external_id' => 'c']], 'next_page_token' => 'T2']),
            F::json(200, ['customers' => [['external_id' => 'd']], 'next_page_token' => '']),
        );
    }

    public function testIteratorWalksEveryPage(): void
    {
        $this->pages();
        $ids = [];
        foreach ($this->client()->customers->listCustomersIterator(pageSize: 2) as $customer) {
            $ids[] = $customer->externalId;
        }

        self::assertSame(['a', 'b', 'c', 'd'], $ids);
        self::assertCount(3, $this->transport->requests);
        self::assertStringEndsWith('/v1/customers?page_size=2', $this->transport->requests[0]->url);
        self::assertStringEndsWith('/v1/customers?page_size=2&page_token=T1', $this->transport->requests[1]->url);
        self::assertStringEndsWith('/v1/customers?page_size=2&page_token=T2', $this->transport->requests[2]->url);
    }

    public function testPagesYieldsTheResponses(): void
    {
        $this->pages();
        $counts = [];
        foreach ($this->client()->customers->listCustomersPages() as $page) {
            $counts[] = \count($page->customers ?? []);
        }

        self::assertSame([2, 1, 1], $counts);
    }

    public function testASinglePageStaysLazy(): void
    {
        $this->pages();
        $gen = $this->client()->customers->listCustomersIterator();
        self::assertCount(0, $this->transport->requests);
        $gen->current();
        self::assertCount(1, $this->transport->requests);
    }

    public function testStartTokenAndFilters(): void
    {
        $this->transport->push(F::json(200, ['invoices' => []]));
        iterator_to_array($this->client()->invoices->listInvoicesIterator(customer: 'c 1', status: 'open', pageToken: 'START'));

        self::assertStringEndsWith('/v1/invoices?customer=c%201&status=open&page_token=START', $this->transport->requests[0]->url);
    }
}
