-- Applies the counter deltas of one batch of one partition, atomically, together with the partition's offset.
--
-- KEYS[1] = lim:off:{p}      next offset applied
-- KEYS[2] = lim:active:{p}   sorted set of the billing-period (lim:c) and long-window (lim:y) counter keys, score = last update (ms)
-- KEYS[3] = lim:wactive:{p}  the same for the short-window counter keys (lim:w)
-- ARGV[1] first offset of the batch      ARGV[2] next offset after the batch
-- ARGV[3] now (ms)                       ARGV[4] offset the caller expects to find (0 = none)
-- ARGV[5] n counters                     ARGV[6] n tenant timestamps
-- then n counters, each (counter key, op, delta, expireat; 0 = never expires) followed by the extras of its op, and n * (tenant ts key, ts ms)
--
--   op 'I'  adds delta.                                            no extras
--   op 'M'  keeps the greater of the counter and delta (MAX).      no extras
--   op 'L'  sets the counter to delta if the event is later than the one the counter holds (LAST). The event is (micros, id), ordered by micros and
--           then by id bytewise, and is kept in lim:ord:{key} as "micros:id".     extras: micros, id
--   op 'U'  adds the members that are new to the set lim:set:{key} to the counter (UNIQUE): the counter is the number of members, and a member is the
--           first 16 bytes of the SHA-256 of a value. A full set (cap members) takes no more, and reconciliation brings the counter to the exact count.
--           delta is unused.                                       extras: cap, n members, the members
--
-- {key} in lim:ord:{key} and lim:set:{key} is the counter key without its "lim:" prefix, and both keys share the counter's expiry.
--
-- Returns {'OK', {key, before, after} ...} or one of {'SKIP'} {'PARTIAL', applied} {'GAP', applied} {'RESET'}. A row {key, 'OVERFLOW', 'OVERFLOW'}
-- is a counter that was left alone because it would pass 2^53, and {key, 'FULL', 'FULL'} a UNIQUE set that had no room for a new member.
-- Numbers are Lua doubles, exact below 2^53; Go guarantees deltas below that and the script refuses to cross it.
local LIMIT = 9007199254740992

-- a > b, bytewise (the < of Lua strings follows the locale of the server)
local function after_bytes(a, b)
  local n = math.min(#a, #b)
  for k = 1, n do
    local x, y = string.byte(a, k), string.byte(b, k)
    if x ~= y then return x > y end
  end
  return #a > #b
end

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
  local companions = {}
  if op == 'M' then
    if d > before then
      redis.call('SET', key, delta)
      after = d
    end
  elseif op == 'L' then
    local micros, id = tonumber(ARGV[i]), ARGV[i + 1]
    i = i + 2
    local okey = 'lim:ord:' .. string.sub(key, 5)
    companions[1] = okey
    local win = true
    local held = redis.call('GET', okey)
    if held then
      local sep = string.find(held, ':', 1, true)
      local hm, hid = tonumber(string.sub(held, 1, sep - 1)), string.sub(held, sep + 1)
      win = micros > hm or (micros == hm and after_bytes(id, hid))
    end
    if win then
      redis.call('SET', key, delta)
      redis.call('SET', okey, string.format('%.0f', micros) .. ':' .. id)
      after = d
    end
  elseif op == 'U' then
    local cap, n = tonumber(ARGV[i]), tonumber(ARGV[i + 1])
    i = i + 2
    local skey = 'lim:set:' .. string.sub(key, 5)
    companions[1] = skey
    local added, full = 0, false
    for _ = 1, n do
      local member = ARGV[i]
      i = i + 1
      if redis.call('SISMEMBER', skey, member) == 0 then
        if redis.call('SCARD', skey) < cap then
          redis.call('SADD', skey, member)
          added = added + 1
        else
          full = true
        end
      end
    end
    if full then out[#out + 1] = {key, 'FULL', 'FULL'} end
    if added > 0 then
      if before + added > LIMIT then
        ok = false
        out[#out + 1] = {key, 'OVERFLOW', 'OVERFLOW'}
      else
        after = redis.call('INCRBY', key, added)
      end
    end
  elseif before + d > LIMIT then
    ok = false
    out[#out + 1] = {key, 'OVERFLOW', 'OVERFLOW'}
  else
    after = redis.call('INCRBY', key, delta)
  end
  if ok then
    if tonumber(exp) > 0 then
      redis.call('EXPIREAT', key, exp)
      for _, c in ipairs(companions) do redis.call('EXPIREAT', c, exp) end
    else
      redis.call('PERSIST', key)
      for _, c in ipairs(companions) do redis.call('PERSIST', c) end
    end
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
