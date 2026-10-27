<?php

declare(strict_types=1);

namespace Payrudder\Exception;

/**
 * HTTP 401: bad or missing API key.
 */
class AuthenticationException extends ApiException {}
