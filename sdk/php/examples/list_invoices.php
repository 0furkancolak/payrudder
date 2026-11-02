<?php

// List a customer's paid invoices. The iterator follows the page token for you and yields invoices, not pages.

declare(strict_types=1);

use Payrudder\Client;

require __DIR__ . '/../vendor/autoload.php'; // in your project: vendor/autoload.php

$client = new Client();

foreach ($client->invoices->listInvoicesIterator(customer: 'cus_42', status: 'paid', pageSize: 100) as $invoice) {
    // totalMinor is an int in the currency's minor units (cents, kurus), never a float.
    echo "{$invoice->number} {$invoice->periodId} {$invoice->totalMinor} {$invoice->currency}\n";
}
