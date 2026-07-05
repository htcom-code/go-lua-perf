-- measure.lua — PUC-Lua side of the lua-pure vs PUC-Lua compile comparison.
--
-- Usage: lua measure.lua <script.lua> [reps]
-- Prints one line "<compile_ms> <retained_mb>" to stdout:
--   compile_ms  — best (min) wall time of load() over reps, i.e. parse + compile
--                 the source into an executable chunk (NOT running it).
--   retained_mb — heap held by the loaded chunk (its function prototype tree),
--                 measured via collectgarbage("count") with the chunk kept alive.
--
-- This mirrors what the Go side measures for lua-pure (parse + compile into a
-- *Proto, then the retained size of that proto), so the two engines are
-- compared apples-to-apples.

local path = arg[1]
local reps = tonumber(arg[2]) or 5
assert(path, "usage: lua measure.lua <script.lua> [reps]")

local fh = assert(io.open(path, "r"))
local src = fh:read("a")
fh:close()

-- Compile time: minimum over reps (min rejects scheduler/GC noise).
local best = math.huge
for _ = 1, reps do
	collectgarbage("collect")
	local t0 = os.clock()
	local chunk = assert(load(src, "@" .. path))
	local dt = (os.clock() - t0) * 1000
	if dt < best then best = dt end
	chunk = nil -- drop so the next rep starts clean
end

-- Retained: heap delta from holding one loaded chunk (the compiled proto tree),
-- without running it — matches the lua-pure proto-only measurement.
collectgarbage("collect")
collectgarbage("collect")
local before = collectgarbage("count")
local keep = assert(load(src, "@" .. path))
collectgarbage("collect")
collectgarbage("collect")
local after = collectgarbage("count")
local _ = keep -- keep alive past the measurement

io.write(string.format("%.3f %.3f\n", best, (after - before) / 1024))
