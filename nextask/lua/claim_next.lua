local prefix = ARGV[1]
local timestamp = ARGV[2]
local new_status = ARGV[3]

local check_limit = 1000
local status_key = 'status:pending'
local paths = redis.call('ZRANGE', status_key, 0, check_limit - 1)

for _, path in ipairs(paths) do
    if string.sub(path, 1, #prefix) == prefix then
        local record_key = 'record:' .. path
        local current_status = redis.call('HGET', record_key, 'status')
        
        if current_status == 'pending' then
            redis.call('HSET', record_key, 'status', new_status)
            redis.call('HSET', record_key, 'updated_at', timestamp)
            redis.call('ZREM', status_key, path)
            redis.call('ZADD', 'status:' .. new_status, timestamp, path)
            return path
        else
            redis.call('ZREM', status_key, path)
            if not current_status then
                redis.call('SREM', 'records:index', path)
            end
        end
    end
end

return nil
