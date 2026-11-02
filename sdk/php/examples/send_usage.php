<?php

// Send one usage event. PAYRUDDER_API_KEY (qk_test_... for the sandbox) and PAYRUDDER_BASE_URL are read from the environment.

declare(strict_types=1);

use Payrudder\Client;
use Payrudder\Enum\EventResultStatus;

require __DIR__ . '/../vendor/autoload.php'; // in your project: vendor/autoload.php

$client = new Client();

$results = $client->events->ingest([
    [
        'customer_external_id' => 'cus_42',
        'meter_code' => 'api_calls',
        'quantity' => '1',
        'event_time' => gmdate('c'),
        // Name the thing that happened (a request id, a job id) so a retry never counts twice. A live key must send one.
        'idempotency_key' => 'request-8f2c',
    ],
]);

$result = $results[0];
if ($result->status === EventResultStatus::Rejected) {
    fwrite(STDERR, "rejected: {$result->errorMessage}\n");
    exit(1);
}
echo $result->eventId, "\n"; // accepted, or a duplicate of an event counted before
