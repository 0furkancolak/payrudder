<?php

declare(strict_types=1);

namespace Payrudder\Exception;

/**
 * HTTP 429: retry after the delay in retryAfter.
 */
class RateLimitException extends ApiException {}
