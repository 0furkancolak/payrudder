<?php

declare(strict_types=1);

namespace Payrudder\Exception;

/**
 * HTTP 400 or 422: the request was refused as invalid.
 */
class ValidationException extends ApiException {}
