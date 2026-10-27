<?php

declare(strict_types=1);

namespace Payrudder\Exception;

/**
 * The SDK is configured wrongly (missing API key, no usable HTTP transport).
 */
class ConfigurationException extends PayrudderException {}
