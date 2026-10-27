<?php

declare(strict_types=1);

namespace Payrudder\Tests\Support;

use Payrudder\Client;
use PHPUnit\Framework\TestCase as BaseTestCase;

abstract class TestCase extends BaseTestCase
{
    /** @var list<float> */
    protected array $sleeps = [];

    protected FakeTransport $transport;

    protected function setUp(): void
    {
        $this->sleeps = [];
        $this->transport = new FakeTransport();
    }

    protected function client(int $maxRetries = 2): Client
    {
        return new Client(
            apiKey: 'qk_test_abcdefgh_secret',
            baseUrl: 'https://api.example.test/',
            maxRetries: $maxRetries,
            transport: $this->transport,
            sleeper: function (float $s): void {
                $this->sleeps[] = $s;
            },
        );
    }
}
