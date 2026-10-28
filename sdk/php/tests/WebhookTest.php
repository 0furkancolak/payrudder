<?php

declare(strict_types=1);

namespace Payrudder\Tests;

use Payrudder\Exception\WebhookException;
use Payrudder\Tools\SpecLoader;
use Payrudder\Webhook\Data\CreditsLowEventData;
use Payrudder\Webhook\Data\InvoiceEventData;
use Payrudder\Webhook\Data\PaymentLinkEventData;
use Payrudder\Webhook\Webhook;
use Payrudder\Webhook\WebhookEvent;
use Payrudder\Webhook\WebhookEventType;
use PHPUnit\Framework\TestCase;

final class WebhookTest extends TestCase
{
    private const SECRET = 'whsec_test_secret';
    private const NOW = 1_700_000_000;
    private const BODY = '{"id":"0192","type":"invoice.paid","created":"2026-10-20T10:00:00Z","tenant_id":"t1","data":{"invoice_id":"i1","total_minor":"1116","tax_minor":"186"}}';

    private static function sign(string $body, string $secret, int $t): string
    {
        return hash_hmac('sha256', $t . '.' . $body, $secret);
    }

    public function testValidSignature(): void
    {
        $header = 't=' . self::NOW . ',v1=' . self::sign(self::BODY, self::SECRET, self::NOW);
        Webhook::verify(self::BODY, $header, self::SECRET, now: self::NOW + 10);
        self::assertTrue(Webhook::isValid(self::BODY, $header, self::SECRET, now: self::NOW));
    }

    public function testTamperedPayloadIsRejected(): void
    {
        $header = 't=' . self::NOW . ',v1=' . self::sign(self::BODY, self::SECRET, self::NOW);
        self::assertFalse(Webhook::isValid(str_replace('1116', '1', self::BODY), $header, self::SECRET, now: self::NOW));
        // re-encoded JSON is a different byte string
        self::assertFalse(Webhook::isValid((string) json_encode(json_decode(self::BODY), JSON_PRETTY_PRINT), $header, self::SECRET, now: self::NOW));
    }

    public function testWrongSecretIsRejected(): void
    {
        $header = 't=' . self::NOW . ',v1=' . self::sign(self::BODY, self::SECRET, self::NOW);
        self::assertFalse(Webhook::isValid(self::BODY, $header, 'whsec_other', now: self::NOW));
    }

    public function testStaleAndFutureTimestampsAreRejected(): void
    {
        $header = 't=' . self::NOW . ',v1=' . self::sign(self::BODY, self::SECRET, self::NOW);
        self::assertTrue(Webhook::isValid(self::BODY, $header, self::SECRET, now: self::NOW + 300));
        self::assertFalse(Webhook::isValid(self::BODY, $header, self::SECRET, now: self::NOW + 301));
        self::assertFalse(Webhook::isValid(self::BODY, $header, self::SECRET, now: self::NOW - 301));
        self::assertTrue(Webhook::isValid(self::BODY, $header, self::SECRET, tolerance: 3600, now: self::NOW + 3000));
        $this->expectException(WebhookException::class);
        $this->expectExceptionMessage('tolerance');
        Webhook::verify(self::BODY, $header, self::SECRET, now: self::NOW + 1000);
    }

    public function testTheTimestampIsPartOfTheSignature(): void
    {
        // a valid signature for t=NOW cannot be replayed with a fresh timestamp
        $sig = self::sign(self::BODY, self::SECRET, self::NOW);
        self::assertFalse(Webhook::isValid(self::BODY, 't=' . (self::NOW + 1000) . ',v1=' . $sig, self::SECRET, now: self::NOW + 1000));
    }

    public function testRotatedSecretAcceptsEitherSignature(): void
    {
        $old = self::sign(self::BODY, 'whsec_old', self::NOW);
        $new = self::sign(self::BODY, 'whsec_new', self::NOW);
        $header = 't=' . self::NOW . ',v1=' . $new . ',v1=' . $old;

        self::assertTrue(Webhook::isValid(self::BODY, $header, 'whsec_old', now: self::NOW));
        self::assertTrue(Webhook::isValid(self::BODY, $header, 'whsec_new', now: self::NOW));
        self::assertFalse(Webhook::isValid(self::BODY, $header, 'whsec_third', now: self::NOW));
    }

    public function testMalformedHeaders(): void
    {
        foreach (['', 'garbage', 't=abc,v1=00', 'v1=' . str_repeat('0', 64), 't=' . self::NOW, 't=-5,v1=00', 't=' . self::NOW . ',v1='] as $h) {
            self::assertFalse(Webhook::isValid(self::BODY, $h, self::SECRET, now: self::NOW), "header '$h'");
        }
    }

    public function testUppercaseHexAndSpacesAreTolerated(): void
    {
        $sig = strtoupper(self::sign(self::BODY, self::SECRET, self::NOW));
        self::assertTrue(Webhook::isValid(self::BODY, 't=' . self::NOW . ', v1=' . $sig, self::SECRET, now: self::NOW));
    }

    public function testConstructEvent(): void
    {
        $header = 't=' . self::NOW . ',v1=' . self::sign(self::BODY, self::SECRET, self::NOW);
        $event = Webhook::constructEvent(self::BODY, $header, self::SECRET, now: self::NOW);

        self::assertSame('0192', $event->id);
        self::assertSame(WebhookEventType::INVOICE_PAID, $event->type);
        self::assertSame('t1', $event->tenantId);
        self::assertSame('1116', $event->data['total_minor']); // money stays a string of minor units
    }

    public function testConstructEventRejectsABodyThatIsNotAnEvent(): void
    {
        $body = '[1,2]';
        $header = 't=' . self::NOW . ',v1=' . self::sign($body, self::SECRET, self::NOW);
        $this->expectException(WebhookException::class);
        Webhook::constructEvent($body, $header, self::SECRET, now: self::NOW);
    }

    public function testEventTypeConstantsMatchTheSpecAndTheDocs(): void
    {
        $spec = SpecLoader::load();
        $enum = array_values(array_filter((array) $spec['components']['schemas']['WebhookEventType']['enum'], 'is_string'));
        $mine = WebhookEventType::all();
        sort($enum);
        sort($mine);
        self::assertSame($enum, $mine);

        $docs = (string) file_get_contents(\dirname(__DIR__, 3) . '/docs/16-webhooks.md');
        foreach (WebhookEventType::all() as $type) {
            self::assertStringContainsString('`' . $type . '`', $docs, "docs/16-webhooks.md does not list $type");
        }
    }

    public function testDataAsReadsTheEventFamily(): void
    {
        $invoice = WebhookEvent::fromJson(self::BODY)->dataAs(InvoiceEventData::class);
        self::assertSame('i1', $invoice->invoiceId);
        self::assertSame(1116, $invoice->totalMinor);
        self::assertSame(186, $invoice->taxMinor);
        self::assertNull($invoice->netMinor);

        $link = WebhookEvent::fromJson('{"id":"1","type":"payment_link.paid","data":{"link_id":"l1","single_use":true,"paid_count":"2"}}')
            ->dataAs(PaymentLinkEventData::class);
        self::assertSame('l1', $link->linkId);
        self::assertTrue($link->singleUse);
        self::assertSame(2, $link->paidCount);

        $low = WebhookEvent::fromJson('{"id":"2","type":"credits.low","data":{"customer_id":"c1","balance":"4.5","threshold":"5"}}')
            ->dataAs(CreditsLowEventData::class);
        self::assertSame('4.5', $low->balance);
        self::assertNull($low->customerExternalId);
    }
}
