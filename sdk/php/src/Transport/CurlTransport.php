<?php

declare(strict_types=1);

namespace Payrudder\Transport;

use CurlHandle;
use Payrudder\Exception\ConfigurationException;
use Payrudder\Exception\ConnectionException;
use Payrudder\Exception\TimeoutException;

/**
 * The default transport: ext-curl, no other package needed. Redirects are not followed, so the
 * API key never travels to a host the caller did not name.
 */
final class CurlTransport implements TransportInterface
{
    public function __construct()
    {
        if (!\extension_loaded('curl')) {
            throw new ConfigurationException('ext-curl is not loaded; install it or pass a PSR-18 client');
        }
    }

    public function send(HttpRequest $request): HttpResponse
    {
        if ($request->url === '' || $request->method === '') {
            throw new ConnectionException('a request needs a method and a URL');
        }
        $ch = curl_init();
        $headers = [];
        foreach ($request->headers as $name => $value) {
            $headers[] = $name . ': ' . $value;
        }
        /** @var array<string,string> $received */
        $received = [];

        curl_setopt_array($ch, [
            CURLOPT_URL => $request->url,
            CURLOPT_CUSTOMREQUEST => $request->method,
            CURLOPT_HTTPHEADER => $headers,
            CURLOPT_RETURNTRANSFER => true,
            CURLOPT_FOLLOWLOCATION => false,
            CURLOPT_PROTOCOLS => CURLPROTO_HTTP | CURLPROTO_HTTPS,
            CURLOPT_TIMEOUT_MS => max(1, (int) round($request->timeout * 1000)),
            CURLOPT_CONNECTTIMEOUT_MS => max(1, (int) round(min($request->timeout, 10.0) * 1000)),
            CURLOPT_HEADERFUNCTION => static function (CurlHandle $_, string $line) use (&$received): int {
                $pos = strpos($line, ':');
                if ($pos !== false) {
                    $name = strtolower(trim(substr($line, 0, $pos)));
                    $value = trim(substr($line, $pos + 1));
                    $received[$name] = isset($received[$name]) ? $received[$name] . ', ' . $value : $value;
                } elseif (str_starts_with($line, 'HTTP/')) {
                    $received = []; // a new response block (after 100-continue or a proxy)
                }

                return \strlen($line);
            },
        ]);
        if ($request->body !== null) {
            curl_setopt($ch, CURLOPT_POSTFIELDS, $request->body);
        }
        if ($request->method === 'HEAD') {
            curl_setopt($ch, CURLOPT_NOBODY, true);
        }

        $body = curl_exec($ch);
        $errno = curl_errno($ch);
        $error = curl_error($ch);
        $status = (int) curl_getinfo($ch, CURLINFO_RESPONSE_CODE);

        if ($body === false || $errno !== 0) {
            $message = 'request to ' . $request->url . ' failed: ' . ($error !== '' ? $error : 'curl error ' . $errno);
            if ($errno === CURLE_OPERATION_TIMEDOUT) {
                throw new TimeoutException($message);
            }
            throw new ConnectionException($message);
        }

        return new HttpResponse($status, $received, \is_string($body) ? $body : '');
    }
}
