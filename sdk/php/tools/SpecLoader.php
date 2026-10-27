<?php

declare(strict_types=1);

namespace Payrudder\Tools;

use RuntimeException;
use Symfony\Component\Yaml\Yaml;

/**
 * Reads api/openapi/v1.yaml for the generator and the drift test.
 *
 * The spec is plain YAML that every parser reads the same way (descriptions with commas or apostrophes are quoted), so it is parsed as it is.
 */
final class SpecLoader
{
    public static function defaultPath(): string
    {
        return \dirname(__DIR__, 3) . '/api/openapi/v1.yaml';
    }

    /**
     * @return array{paths: array<string,array<string,array<string,mixed>>>, components: array{schemas: array<string,array<string,mixed>>}}
     */
    public static function load(?string $path = null): array
    {
        $path ??= self::defaultPath();
        $text = file_get_contents($path);
        if ($text === false) {
            throw new RuntimeException("cannot read $path");
        }

        /** @var array{paths: array<string,array<string,array<string,mixed>>>, components: array{schemas: array<string,array<string,mixed>>}} $spec */
        $spec = Yaml::parse($text);

        return $spec;
    }
}
