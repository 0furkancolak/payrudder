<?php

// Create a customer. The external id is the customer's natural key: creating it twice answers 409 instead of making a second customer.

declare(strict_types=1);

use Payrudder\Client;
use Payrudder\Exception\ConflictException;

require __DIR__ . '/../vendor/autoload.php'; // in your project: vendor/autoload.php

$client = new Client();

try {
    $customer = $client->customers->createCustomer(['external_id' => 'cus_42', 'name' => 'Ada Lovelace', 'email' => 'ada@example.com']);
    echo "created {$customer->externalId}\n";
} catch (ConflictException) {
    echo "cus_42 exists already\n";
}
