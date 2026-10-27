<?php

declare(strict_types=1);

namespace Payrudder\Contract;

/**
 * A typed object that serialises to the JSON the API expects (snake_case keys).
 */
interface RequestBody
{
    /**
     * @return array<string,mixed>
     */
    public function toArray(): array;
}
