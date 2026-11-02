<?php

declare(strict_types=1);

namespace App\Http\Controllers;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Illuminate\Support\Facades\Log;
use Payrudder\Exception\WebhookException;
use Payrudder\Webhook\Webhook;

final class WebhookController
{
    public function __invoke(Request $request): JsonResponse
    {
        try {
            // The signature covers the exact bytes Payrudder sent: getContent() is the raw body, never json_encode($request->all()).
            $event = Webhook::constructEvent($request->getContent(), (string) $request->header('Payrudder-Signature'), (string) config('payrudder.webhook_secret'));
        } catch (WebhookException $e) {
            return response()->json(['error' => $e->getMessage()], 400);
        }

        // Do the work after answering (dispatch a job), or the delivery may time out: Payrudder retries one that is not answered with a 2xx.
        Log::info('webhook', ['id' => $event->id, 'type' => $event->type]);

        return response()->json(['id' => $event->id]);
    }
}
