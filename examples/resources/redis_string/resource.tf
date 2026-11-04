# configuration read by the clients, e.g. with `GET app:config`
resource "redis_string" "config" {
  key = "app:config"
  value = jsonencode({
    feature_flags = { new_checkout = true }
    max_items     = 50
  })
}
