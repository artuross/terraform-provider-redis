#!lua name=counters

-- FCALL count 1 <key> [increment]
local function count(keys, args)
	return redis.call('INCRBY', keys[1], tonumber(args[1] or 1))
end

redis.register_function('count', count)
