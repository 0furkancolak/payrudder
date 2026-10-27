<?php

declare(strict_types=1);

namespace Payrudder\Transport;

use Payrudder\Exception\ConnectionException;

/**
 * Sends one request and returns whatever status the server answered, error statuses included.
 * Anything that stops an answer from arriving is a ConnectionException (TimeoutException for a timeout).
 */
interface TransportInterface
{
    /**
     * @throws ConnectionException
     */
    public function send(HttpRequest $request): HttpResponse;
}
