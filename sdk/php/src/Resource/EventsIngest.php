<?php

declare(strict_types=1);

namespace Payrudder\Resource;

use InvalidArgumentException;
use Payrudder\Contract\RequestBody;
use Payrudder\Exception\PayrudderException;
use Payrudder\Exception\UnexpectedResponseException;
use Payrudder\Internal\IngestBatcher;
use Payrudder\Model\EventResult;
use Payrudder\Model\IngestEventsResponse;
use Payrudder\RequestOptions;

/**
 * The ingest helper of `$client->events`.
 */
trait EventsIngest
{
    /**
     * Sends any number of events in as many requests as the limits need (at most 500 events and
     * 1 MiB each), in order.
     *
     * An event without an `idempotency_key` gets a generated one before the first attempt, so the
     * automatic retries of a call can never count an event twice (live keys require one). A
     * rejected event is not an error: its result says `rejected` with an `error_code`. A failing
     * batch (auth, 429 after the retries, ...) throws; batches sent before it stay sent, and
     * calling again with the same keys is safe.
     *
     * @param iterable<RequestBody|array<string,mixed>> $events
     *
     * @return list<EventResult> one per event, in request order
     *
     * @throws InvalidArgumentException when a single event alone is larger than a request may be
     * @throws PayrudderException
     */
    public function ingest(iterable $events, ?RequestOptions $options = null): array
    {
        $results = [];
        foreach (IngestBatcher::batches($events) as $batch) {
            $answer = IngestEventsResponse::fromArray($this->http->json('POST', '/v1/events', [], ['events' => $batch], $options, true));
            $got = $answer->results ?? [];
            if (\count($got) !== \count($batch)) {
                throw new UnexpectedResponseException(\sprintf('sent %d events, got %d results', \count($batch), \count($got)));
            }
            array_push($results, ...$got);
        }

        return $results;
    }
}
