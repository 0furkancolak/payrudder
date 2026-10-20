-- Kafka offsets of the limit alert consumer.

-- name: LoadOffsets :many
SELECT partition, next_offset FROM kafka_offsets
WHERE consumer_group = @consumer_group AND topic = @topic AND partition = ANY(@partitions::int[]);

-- name: SaveOffset :exec
INSERT INTO kafka_offsets (consumer_group, topic, partition, next_offset)
VALUES (@consumer_group, @topic, @partition, @next_offset)
ON CONFLICT (consumer_group, topic, partition)
DO UPDATE SET next_offset = GREATEST(kafka_offsets.next_offset, EXCLUDED.next_offset), updated_at = now();
