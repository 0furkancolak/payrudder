<?php

declare(strict_types=1);

namespace Payrudder\Webhook\Data;

use Payrudder\Internal\Cast;

/**
 * The `data` of `contract.accepted`: one event for the versions a customer accepted together.
 * Read it with `WebhookEvent::dataAs(ContractAcceptedEventData::class)`.
 */
final class ContractAcceptedEventData implements EventData
{
    /**
     * @param list<array<string,mixed>> $items each: acceptance_id, kind, audience, version_id, version, sha256, locale
     * @param array<string,mixed> $context kind (`hosted_invoice` or `api`) and ref
     */
    public function __construct(
        public readonly string $groupId,
        public readonly string $source,
        public readonly string $acceptedAt,
        public readonly string $snapshotSha256,
        public readonly array $items,
        public readonly array $context,
        public readonly bool $immediateStartConsent = false,
        public readonly bool $withdrawalWaiverAcknowledged = false,
        public readonly ?string $customerId = null,
        public readonly ?string $customerExternalId = null,
    ) {}

    /**
     * @param array<string,mixed> $data
     */
    public static function fromArray(array $data): self
    {
        $items = [];
        foreach (Cast::list($data['items'] ?? null) ?? [] as $item) {
            $map = Cast::map($item);
            if ($map !== null) {
                $items[] = $map;
            }
        }

        return new self(
            groupId: Cast::string($data['group_id'] ?? null) ?? '',
            source: Cast::string($data['source'] ?? null) ?? '',
            acceptedAt: Cast::string($data['accepted_at'] ?? null) ?? '',
            snapshotSha256: Cast::string($data['snapshot_sha256'] ?? null) ?? '',
            items: $items,
            context: Cast::map($data['context'] ?? null) ?? [],
            immediateStartConsent: Cast::bool($data['immediate_start_consent'] ?? null) ?? false,
            withdrawalWaiverAcknowledged: Cast::bool($data['withdrawal_waiver_acknowledged'] ?? null) ?? false,
            customerId: Cast::string($data['customer_id'] ?? null),
            customerExternalId: Cast::string($data['customer_external_id'] ?? null),
        );
    }
}
