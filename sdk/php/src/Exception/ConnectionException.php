<?php

declare(strict_types=1);

namespace Payrudder\Exception;

/**
 * The request never produced an HTTP answer (DNS, refused connection, TLS, reset).
 */
class ConnectionException extends PayrudderException {}
