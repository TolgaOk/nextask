-- Atomically claim the next unfinished run by marking it as running
--
-- ATOMICITY GUARANTEE:
-- Redis executes this entire Lua script atomically. No other commands can
-- run until this completes, preventing all race conditions. Multiple workers
-- calling this concurrently will each get unique runs.
--
-- KEYS: (empty - we use ARGV for flexibility)
-- ARGV[1]: path prefix to match
-- ARGV[2]: current timestamp (float as string)
-- ARGV[3]: new status value (should be "running")
--
-- Returns: claimed run path (string) or nil if no runs available
--
-- Design: Checks up to 1000 oldest runs per status. This balances:
-- - Memory usage (~50KB max)
-- - Coverage (finds match if in first 1000)
-- - Simplicity (no complex batching logic)
--
-- Edge cases handled:
-- 1. No unfinished runs for given prefix
-- 2. Stale index entries (run deleted or status changed)
-- 3. Empty sorted sets

local prefix = ARGV[1]
local timestamp = ARGV[2]
local new_status = ARGV[3]

-- Check up to 1000 oldest runs per status
-- This prevents loading millions of runs while covering typical cases
local check_limit = 1000

-- Priority order: pending first, then failed runs
local statuses = {'pending', 'failed'}

for _, old_status in ipairs(statuses) do
    local status_key = 'status:' .. old_status
    
    -- Get oldest runs (by timestamp) from this status
    local paths = redis.call('ZRANGE', status_key, 0, check_limit - 1)
    
    for _, path in ipairs(paths) do
        -- Check prefix match
        if string.sub(path, 1, #prefix) == prefix then
            local run_key = 'run:' .. path
            
            -- Verify run exists and has correct status
            local current_status = redis.call('HGET', run_key, 'status')
            
            if current_status == old_status then
                -- ATOMIC CLAIM: These operations execute atomically together
                redis.call('HSET', run_key, 'status', new_status)
                redis.call('HSET', run_key, 'updated_at', timestamp)
                redis.call('ZREM', status_key, path)
                redis.call('ZADD', 'status:' .. new_status, timestamp, path)
                
                return path
            else
                -- Self-healing: clean up stale index
                redis.call('ZREM', status_key, path)
                if not current_status then
                    redis.call('SREM', 'runs:index', path)
                end
            end
        end
    end
end

return nil
