<?php

declare(strict_types=1);

namespace Payrudder\Webhook\Data;

use Payrudder\Internal\Cast;

/**
 * The `data` of `contract.version_published`.
 * Read it with `WebhookEvent::dataAs(ContractVersionPublishedEventData::class)`.
 */
final class ContractVersionPublishedEventData implements EventData
{
    /**
     * @param list<string> $locales
     */
    public function __construct(
        public readonly string $versionId,
        public readonly string $kind,
        public readonly string $audience,
        public readonly int $number,
        public readonly bool $material,
        public readonly string $sha256,
        public readonly array $locales,
        public readonly string $publishedAt,
    ) {}

    /**
     * @param array<string,mixed> $data
     */
    public static function fromArray(array $data): self
    {
        return new self(
            versionId: Cast::string($data['version_id'] ?? null) ?? '',
            kind: Cast::string($data['kind'] ?? null) ?? '',
            audience: Cast::string($data['audience'] ?? null) ?? '',
            number: Cast::int($data['number'] ?? null) ?? 0,
            material: Cast::bool($data['material'] ?? null) ?? false,
            sha256: Cast::string($data['sha256'] ?? null) ?? '',
            locales: Cast::strings($data['locales'] ?? null) ?? [],
            publishedAt: Cast::string($data['published_at'] ?? null) ?? '',
        );
    }
}
