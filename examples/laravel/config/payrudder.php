<?php

// Read here and nowhere else: env() returns null outside config files once `php artisan config:cache` has run. The SDK's own reading of PAYRUDDER_API_KEY would not see
// a value that only .env holds (Laravel does not put .env values into the process environment), so the controller passes these to the client.
return [
    'api_key' => env('PAYRUDDER_API_KEY'),
    'base_url' => env('PAYRUDDER_BASE_URL'),
    // The secret (whsec_...) the webhook endpoint was created with.
    'webhook_secret' => env('PAYRUDDER_WEBHOOK_SECRET', ''),
];
