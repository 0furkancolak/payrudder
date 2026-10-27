<?php

declare(strict_types=1);

namespace Payrudder\Exception;

/**
 * The server answered 2xx with something that is not what the contract promises.
 */
class UnexpectedResponseException extends PayrudderException {}
