/**
 * int64 values (every `*_minor` amount) travel as JSON strings. A JS `number` would silently lose precision beyond
 * 2^53, so responses keep the string and requests accept a string or a `bigint`.
 */
export type Int64 = string;
export type Int64Input = string | bigint;

/** Parses an int64 string (or passes a bigint through) without going through a lossy number. */
export function toBigInt(value: Int64Input): bigint {
  return typeof value === "bigint" ? value : BigInt(value);
}
