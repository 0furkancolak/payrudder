-- GCRA step, mirroring step() in gcra.go. All times in microseconds.
-- KEYS[1] = gw:rl:{tenant}:{class}
-- ARGV[1] = emission interval, ARGV[2] = burst, ARGV[3] = cost, ARGV[4] = now (0 = use Redis TIME; tests pass a fixed clock)
-- Returns {allowed (1/0), retry_after}
local emission = tonumber(ARGV[1])
local burst    = tonumber(ARGV[2])
local cost     = tonumber(ARGV[3])
local now      = tonumber(ARGV[4])
if now == 0 then
  local t = redis.call('TIME')
  now = tonumber(t[1]) * 1000000 + tonumber(t[2])
end

local tat = tonumber(redis.call('GET', KEYS[1]) or '0')
if tat < now then tat = now end

local new_tat  = tat + cost * emission
local allow_at = new_tat - burst * emission
if allow_at > now then
  return {0, allow_at - now}
end

-- Expire once the TAT has passed: an idle key costs no memory.
redis.call('SET', KEYS[1], string.format('%d', new_tat), 'PX', math.ceil((new_tat - now) / 1000) + 1)
return {1, 0}
