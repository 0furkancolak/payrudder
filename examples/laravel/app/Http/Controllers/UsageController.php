<?php

declare(strict_types=1);

namespace App\Http\Controllers;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Str;
use Payrudder\Client;
use Payrudder\Exception\PayrudderException;

final class UsageController
{
    /** POST /usage {"customer": "cus_42", "meter": "api_calls", "quantity": "1"} counts one event for that customer. */
    public function __invoke(Request $request): JsonResponse
    {
        $input = $request->validate(['customer' => 'required|string', 'meter' => 'required|string', 'quantity' => 'sometimes|numeric']);

        try {
            $client = new Client(apiKey: config('payrudder.api_key'), baseUrl: config('payrudder.base_url'));
            $results = $client->events->ingest([
                [
                    'customer_external_id' => $input['customer'],
                    'meter_code' => $input['meter'],
                    'quantity' => (string) ($input['quantity'] ?? '1'),
                    'event_time' => gmdate('c'),
                    // A key per request makes a retry of this call count once. A real app derives it from what happened (an order id), not from the clock.
                    'idempotency_key' => $request->header('Idempotency-Key') ?? (string) Str::uuid(),
                ],
            ]);
        } catch (PayrudderException $e) {
            return response()->json(['error' => $e->getMessage()], 502);
        }

        return response()->json($results[0]->toArray());
    }
}
