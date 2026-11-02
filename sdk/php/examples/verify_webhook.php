<?php

// Check the signature of a webhook delivery. Run it behind your web server at the address of your endpoint, with PAYRUDDER_WEBHOOK_SECRET set to the endpoint's secret.

declare(strict_types=1);

use Payrudder\Exception\WebhookException;
use Payrudder\Webhook\Webhook;

require __DIR__ . '/../vendor/autoload.php'; // in your project: vendor/autoload.php

$secret = getenv('PAYRUDDER_WEBHOOK_SECRET');
if ($secret === false || $secret === '') {
    http_response_code(500);
    exit('set PAYRUDDER_WEBHOOK_SECRET');
}

// The signature is over the raw bytes: read the body as it arrived, parse it only after it verified.
$payload = (string) file_get_contents('php://input');
$header = $_SERVER['HTTP_PAYRUDDER_SIGNATURE'] ?? '';

try {
    $event = Webhook::constructEvent($payload, is_string($header) ? $header : '', $secret);
} catch (WebhookException) {
    http_response_code(400); // not signed by this endpoint's secret, or too old
    exit;
}

// Deliveries are at least once: ignore an event id you have already handled.
error_log("{$event->id} {$event->type}");
http_response_code(204);
