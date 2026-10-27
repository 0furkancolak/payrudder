<?php

declare(strict_types=1);

namespace Payrudder\Contract;

/**
 * A typed object that can be built from a decoded API answer.
 */
interface Model extends RequestBody
{
    /**
     * @param array<string,mixed> $data decoded JSON object (snake_case keys)
     */
    public static function fromArray(array $data): static;
}
