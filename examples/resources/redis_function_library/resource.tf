resource "redis_function_library" "counters" {
  content = file("${path.module}/counters.lua")
}
