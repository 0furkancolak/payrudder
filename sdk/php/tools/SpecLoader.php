<?php

declare(strict_types=1);

namespace Payrudder\Tools;

use RuntimeException;
use Symfony\Component\Yaml\Yaml;

/**
 * Reads api/openapi/v1.yaml for the generator and the drift test.
 *
 * The spec has unquoted flow-mapping descriptions with commas or apostrophes
 * (`{ type: string, description: The seller's id, for x }`). That is accepted by the YAML parser
 * of the gateway but not by symfony/yaml, so those descriptions are quoted before parsing.
 * Only descriptions are touched, never structure.
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
        $lines = explode("\n", $text);
        foreach ($lines as $i => $line) {
            if (!str_contains($line, '{') || !str_contains($line, 'description:')) {
                continue;
            }
            $lines[$i] = preg_replace_callback(
                '/(\bdescription:\s*)([^"\'{\[\s][^}]*?)(\s*\}\s*,?\s*)$/',
                static function (array $m): string {
                    if (!str_contains($m[2], ',') && !str_contains($m[2], "'")) {
                        return $m[0];
                    }

                    return $m[1] . '"' . addcslashes($m[2], '"\\') . '"' . $m[3];
                },
                $line,
            ) ?? $line;
        }

        /** @var array{paths: array<string,array<string,array<string,mixed>>>, components: array{schemas: array<string,array<string,mixed>>}} $spec */
        $spec = Yaml::parse(implode("\n", $lines));

        return $spec;
    }
}
