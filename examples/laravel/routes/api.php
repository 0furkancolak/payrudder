<?php

use App\Http\Controllers\UsageController;
use App\Http\Controllers\WebhookController;
use Illuminate\Support\Facades\Route;

Route::get('/healthz', static fn (): string => 'ok');
Route::post('/usage', UsageController::class);
Route::post('/webhooks/payrudder', WebhookController::class);
