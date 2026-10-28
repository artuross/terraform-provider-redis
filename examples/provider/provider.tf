# credentials can only be set with the REDIS_USERNAME and REDIS_PASSWORD environment variables
provider "redis" {
  # rediss:// for TLS
  url = "rediss://redis.example.com:6379"
}
