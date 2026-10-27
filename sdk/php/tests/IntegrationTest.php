<?php

declare(strict_types=1);

namespace Payrudder\Tests;

use Payrudder\Client;
use PHPUnit\Framework\TestCase;

/**
 * Smoke test against a running Payrudder stack. Skipped unless PAYRUDDER_API_KEY is set
 * (PAYRUDDER_BASE_URL defaults to http://localhost:42800). Use a qk_test_ key: it writes a customer.
 */
final class IntegrationTest extends TestCase
{
    protected function setUp(): void
    {
        if ((string) getenv('PAYRUDDER_API_KEY') === '') {
            self::markTestSkipped('PAYRUDDER_API_KEY is not set');
        }
    }

    public function testCreateReadAndListACustomer(): void
    {
        $client = new Client();
        $id = 'sdk-php-' . bin2hex(random_bytes(4));

        $created = $client->customers->createCustomer(['external_id' => $id, 'name' => 'SDK smoke test', 'email' => $id . '@example.com']);
        self::assertSame($id, $created->externalId);
        self::assertSame($id, $client->customers->getCustomer($id)->externalId);

        $found = false;
        foreach ($client->customers->listCustomersIterator(pageSize: 100) as $customer) {
            $found = $found || $customer->externalId === $id;
        }
        self::assertTrue($found);
        $client->meters->listMeters(); // must not throw
        $this->addToAssertionCount(1);
    }
}
