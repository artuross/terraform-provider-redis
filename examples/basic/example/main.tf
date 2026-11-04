terraform {
  required_providers {
    redis = {
      source = "artuross/redis"
    }
  }
}

# credentials come from the REDIS_USERNAME and REDIS_PASSWORD environment variables
provider "redis" {
  url = "redis://localhost:16379"
}

resource "redis_function_library" "counters" {
  content = file("${path.module}/../libraries/counters.lua")
}

resource "redis_string" "config" {
  key = "app:config"
  value = jsonencode({
    max_visits = 100
  })
}

output "library_name" {
  value = redis_function_library.counters.name
}
