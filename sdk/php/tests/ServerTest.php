<?php

declare(strict_types=1);

namespace Payrudder\Tests;

use Payrudder\Client;
use Payrudder\Exception\AuthenticationException;
use Payrudder\Exception\ConnectionException;
use Payrudder\Exception\NotFoundException;
use Payrudder\Exception\TimeoutException;
use Payrudder\Transport\CurlTransport;
use Payrudder\Transport\HttpRequest;
use PHPUnit\Framework\TestCase;

/**
 * The real curl transport against the PHP built-in web server (no network beyond loopback).
 */
final class ServerTest extends TestCase
{
    /** @var resource|null */
    private static $process;

    private static string $base = '';

    private static string $state = '';

    public static function setUpBeforeClass(): void
    {
        $sock = stream_socket_server('tcp://127.0.0.1:0', $errno, $errstr);
        if ($sock === false) {
            self::markTestSkipped('cannot open a local port: ' . $errstr);
        }
        $port = (int) explode(':', (string) stream_socket_get_name($sock, false))[1];
        fclose($sock);

        self::$state = sys_get_temp_dir() . '/payrudder-sdk-test-' . getmypid();
        @unlink(self::$state . '.flaky');
        $cmd = [PHP_BINARY, '-d', 'enable_post_data_reading=0', '-S', "127.0.0.1:$port", __DIR__ . '/Support/router.php'];
        $proc = proc_open($cmd, [0 => ['pipe', 'r'], 1 => ['file', '/dev/null', 'w'], 2 => ['file', '/dev/null', 'w']], $pipes, null, ['PAYRUDDER_TEST_STATE' => self::$state] + getenv());
        if (!\is_resource($proc)) {
            self::markTestSkipped('cannot start php -S');
        }
        self::$process = $proc;
        for ($i = 0; $i < 50; ++$i) {
            $c = @fsockopen('127.0.0.1', $port, $e, $s, 0.1);
            if ($c !== false) {
                fclose($c);
                self::$base = "http://127.0.0.1:$port";

                return;
            }
            usleep(100_000);
        }
        self::markTestSkipped('php -S did not come up');
    }

    public static function tearDownAfterClass(): void
    {
        if (self::$process !== null) {
            proc_terminate(self::$process);
            proc_close(self::$process);
        }
        @unlink(self::$state . '.flaky');
    }

    private function client(float $timeout = 5.0, int $retries = 2): Client
    {
        return new Client(apiKey: 'qk_test_server_secret', baseUrl: self::$base, timeout: $timeout, maxRetries: $retries);
    }

    public function testRetriesA429AndWalksThePages(): void
    {
        $ids = [];
        foreach ($this->client()->customers->listCustomersIterator() as $c) {
            $ids[] = $c->externalId;
        }

        self::assertSame(['a', 'b', 'c'], $ids); // the first request was a 429 with Retry-After: 0
    }

    public function testPostSendsTheBodyAndTheUserAgent(): void
    {
        $c = $this->client()->customers->createCustomer(['external_id' => 'c9', 'name' => 'Nine', 'email' => 'n@example.com']);

        self::assertSame('c9', $c->externalId);
        self::assertSame('Nine', $c->name);
    }

    public function testMinorAmountsOverRealHttp(): void
    {
        $inv = $this->client()->invoices->getInvoice('inv1');

        self::assertSame(1116, $inv->totalMinor);
    }

    public function testErrorEnvelope(): void
    {
        try {
            $this->client()->customers->getCustomer('missing');
            self::fail('expected an exception');
        } catch (NotFoundException $e) {
            self::assertSame('plans.customer.not_found', $e->errorKey);
            self::assertSame('req-404', $e->requestId);
        }
    }

    public function testBadKey(): void
    {
        $client = new Client(apiKey: 'wrong', baseUrl: self::$base);
        $this->expectException(AuthenticationException::class);
        $client->meters->listMeters();
    }

    public function testTimeout(): void
    {
        $this->expectException(TimeoutException::class);
        $this->client(timeout: 0.2, retries: 0)->customers->getCustomer('slow');
    }

    public function testConnectionRefused(): void
    {
        $client = new Client(apiKey: 'k', baseUrl: 'http://127.0.0.1:1', maxRetries: 0, timeout: 2.0);
        $this->expectException(ConnectionException::class);
        $client->meters->listMeters();
    }

    public function testRedirectsAreNotFollowed(): void
    {
        $response = (new CurlTransport())->send(new HttpRequest('GET', self::$base . '/v1/redirect', ['Authorization' => 'Bearer qk_test_server_secret'], null, 5.0));

        self::assertSame(302, $response->status);
        self::assertSame('http://127.0.0.1:1/steal', $response->header('Location'));
    }

    public function testMultipartUpload(): void
    {
        $tmp = tempnam(sys_get_temp_dir(), 'pr');
        self::assertIsString($tmp);
        file_put_contents($tmp, 'hello');
        try {
            $r = $this->client()->assets->uploadAssets([$tmp, ['filename' => 'b.txt', 'content' => 'world!', 'content_type' => 'text/plain']], ['files' => ['b.txt' => ['purpose' => 'general']]]);
        } finally {
            unlink($tmp);
        }

        $results = $r->results ?? [];
        self::assertCount(2, $results);
        self::assertSame('b.txt', $results[1]->filename);
        self::assertSame(6, $results[1]->asset?->sizeBytes);
    }
}
