-- Applies the counter deltas of one batch of one partition, atomically, together with the partition's offset.
--
-- KEYS[1] = lim:off:{p}      next offset applied
-- KEYS[2] = lim:active:{p}   sorted set of the billing-period counter keys (lim:c), score = last update (ms)
-- KEYS[3] = lim:wactive:{p}  the same for the window counter keys (lim:w), which reconciliation does not read
-- ARGV[1] first offset of the batch      ARGV[2] next offset after the batch
-- ARGV[3] now (ms)                       ARGV[4] offset the caller expects to find (0 = none)
-- ARGV[5] n counters                     ARGV[6] n tenant timestamps
-- then n * (counter key, op 'I'|'M', delta, expireat) and n * (tenant ts key, ts ms)
--
-- Returns {'OK', {key, before, after} ...} or one of {'SKIP'} {'PARTIAL', applied} {'GAP', applied} {'RESET'}.
-- Numbers are Lua doubles, exact below 2^53; Go guarantees deltas below that and the script refuses to cross it.
local LIMIT = 9007199254740992

local first = tonumber(ARGV[1])
local nxt = tonumber(ARGV[2])
local raw = redis.call('GET', KEYS[1])
if raw then
  local applied = tonumber(raw)
  if applied >= nxt then return {'SKIP'} end
  if applied > first then return {'PARTIAL', raw} end
  if applied < first then return {'GAP', raw} end
elseif tonumber(ARGV[4]) > 0 then
  return {'RESET'}
end

local out = {'OK'}
local now = ARGV[3]
local nc = tonumber(ARGV[5])
local nt = tonumber(ARGV[6])
local i = 7
for _ = 1, nc do
  local key, op, delta, exp = ARGV[i], ARGV[i + 1], ARGV[i + 2], ARGV[i + 3]
  i = i + 4
  local cur = redis.call('GET', key)
  local before = 0
  if cur then before = tonumber(cur) end
  local d = tonumber(delta)
  local after = before
  local ok = true
  if op == 'M' then
    if d > before then
      redis.call('SET', key, delta)
      after = d
    end
  elseif before + d > LIMIT then
    ok = false
    out[#out + 1] = {key, 'OVERFLOW', 'OVERFLOW'}
  else
    after = redis.call('INCRBY', key, delta)
  end
  if ok then
    redis.call('EXPIREAT', key, exp)
    if string.sub(key, 1, 6) == 'lim:w:' then
      redis.call('ZADD', KEYS[3], now, key)
    else
      redis.call('ZADD', KEYS[2], now, key)
    end
    out[#out + 1] = {key, string.format('%.0f', before), string.format('%.0f', after)}
  end
end
for _ = 1, nt do
  local k, ts = ARGV[i], tonumber(ARGV[i + 1])
  i = i + 2
  local cur = redis.call('GET', k)
  if (not cur) or tonumber(cur) < ts then
    redis.call('SET', k, string.format('%.0f', ts))
  end
  redis.call('EXPIRE', k, 3456000)
end
redis.call('SET', KEYS[1], ARGV[2])
return out
