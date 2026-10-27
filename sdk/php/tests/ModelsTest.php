<?php

declare(strict_types=1);

namespace Payrudder\Tests;

use Payrudder\Enum\InvoiceKind;
use Payrudder\Enum\InvoiceStatus;
use Payrudder\Exception\UnexpectedResponseException;
use Payrudder\Internal\Cast;
use Payrudder\Model\CreateCustomerRequest;
use Payrudder\Model\Invoice;
use Payrudder\Tests\Support\FakeTransport as F;
use Payrudder\Tests\Support\TestCase;
use Payrudder\Transport\HttpResponse;

final class ModelsTest extends TestCase
{
    public function testMinorAmountsDecodeFromStringsToInts(): void
    {
        $this->transport->push(F::json(200, [
            'id' => 'inv1',
            'kind' => 'REGULAR',
            'status' => 'paid',
            'subtotal_minor' => '930',
            'total_minor' => '9223372036854775807',
            'lines' => [['line_no' => 1, 'kind' => 'USAGE', 'amount_minor' => '-5', 'quantity' => '1.250']],
        ]));
        $invoice = $this->client()->invoices->getInvoice('inv1');

        self::assertSame(930, $invoice->subtotalMinor);
        self::assertSame(PHP_INT_MAX, $invoice->totalMinor);
        self::assertSame(InvoiceKind::Regular, $invoice->kind);
        self::assertSame(InvoiceStatus::Paid, $invoice->status);
        self::assertSame(-5, $invoice->lines[0]->amountMinor ?? null);
        self::assertSame('1.250', $invoice->lines[0]->quantity ?? null, 'decimals stay strings');
    }

    public function testAnAmountThatDoesNotFitIsAnErrorNotAFloat(): void
    {
        $this->transport->push(F::json(200, '{"id":"x","total_minor":"9223372036854775808"}'));
        $this->expectException(UnexpectedResponseException::class);
        $this->client()->invoices->getInvoice('x');
    }

    public function testBigIntegersInJsonNumbersDoNotBecomeFloats(): void
    {
        // the requester decodes with JSON_BIGINT_AS_STRING: the digits survive
        $this->transport->push(F::json(200, '{"id":"x","total_minor":12345678901234567890}'));
        $this->expectException(UnexpectedResponseException::class);
        $this->client()->invoices->getInvoice('x');
    }

    public function testUnknownEnumValuesSurviveAsStrings(): void
    {
        $invoice = Invoice::fromArray(['status' => 'brand_new_state', 'kind' => 'FEE']);

        self::assertSame('brand_new_state', $invoice->status);
        self::assertSame(InvoiceKind::Fee, $invoice->kind);
    }

    public function testRequestModelsSerialiseToSnakeCaseAndDropUnsetFields(): void
    {
        $body = (new CreateCustomerRequest(externalId: 'cus_1', name: 'Acme', email: 'a@example.com'))->toArray();

        self::assertSame(['external_id' => 'cus_1', 'name' => 'Acme', 'email' => 'a@example.com'], $body);
    }

    public function testAnEmptyBodyIsAnEmptyObject(): void
    {
        $this->transport->push(F::json(200, ['subscription' => ['id' => 's1']]));
        $this->client()->subscriptions->cancelSubscription('s1', creditUnused: true);
        $this->transport->push(F::json(200, ['id' => 's1']));
        $this->client()->subscriptions->applyCoupon('s1', []);

        self::assertSame('{}', $this->transport->requests[1]->body);
    }

    public function testQueryParametersAreEncoded(): void
    {
        $this->transport->push(F::json(200, ['id' => 's1']));
        $this->client()->subscriptions->cancelSubscription('s/1', endTime: '2026-11-03T10:15:00Z', creditUnused: true);

        self::assertSame('DELETE', $this->transport->requests[0]->method);
        self::assertSame('https://api.example.test/v1/subscriptions/s%2F1?end_time=2026-11-03T10%3A15%3A00Z&credit_unused=true', $this->transport->requests[0]->url);
        self::assertNull($this->transport->requests[0]->body);
    }

    public function testCastRefusesLossyIntegers(): void
    {
        self::assertSame(42, Cast::int('42'));
        self::assertSame(42, Cast::int(42.0));
        self::assertNull(Cast::int('4.2'));
        self::assertNull(Cast::int(null));
        self::assertSame('7', Cast::int64(7));
    }

    public function testNoContentAnswers(): void
    {
        $this->transport->push(new HttpResponse(204, [], ''));
        $this->client()->webhookEndpoints->deleteWebhookEndpoint('w1');

        self::assertSame('DELETE', $this->transport->requests[0]->method);
        self::assertSame('https://api.example.test/v1/webhook-endpoints/w1', $this->transport->requests[0]->url);
    }

    public function testCsvIsReturnedAsText(): void
    {
        $this->transport->push(new HttpResponse(200, ['content-type' => 'text/csv'], "a,b\n1,2\n"));

        self::assertSame("a,b\n1,2\n", $this->client()->accounting->exportAccountingCsv(from: '2026-01-01', to: '2026-02-01'));
    }
}
