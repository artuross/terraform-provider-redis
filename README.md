# Terraform Provider for Redis

Manages a single (non-cluster) Redis or Valkey server. Currently, this provider supports only the following features:

- [Strings](https://redis.io/docs/latest/develop/data-types/strings/) as `redis_string` resource.
- [Redis Functions](https://redis.io/docs/latest/develop/programmability/functions-intro/) (Lua scripts) as `redis_function_library` resource.

Supported servers: Redis 7.0+ and Valkey 7.2+. Works with Terraform and OpenTofu.

## Usage

```hcl
terraform {
  required_providers {
    redis = {
      source = "artuross/redis"
    }
  }
}

provider "redis" {
  url = "rediss://redis.example.com:6379"
}

resource "redis_function_library" "counters" {
  content = file("${path.module}/counters.lua")
}
```

```lua
#!lua name=counters

-- FCALL count 1 <key> [increment]
local function count(keys, args)
	return redis.call('INCRBY', keys[1], tonumber(args[1] or 1))
end

redis.register_function('count', count)
```

A runnable example is in [examples/basic](examples/basic).

## Configuration

| Setting | Environment variable | Description                                                     |
| ------- | -------------------- | --------------------------------------------------------------- |
| `url`   | -                    | Required. `redis://host:port`, or `rediss://host:port` for TLS. |
| -       | `REDIS_USERNAME`     | ACL username.                                                   |
| -       | `REDIS_PASSWORD`     | Password.                                                       |

Credentials **can only** be set with environment variables, so they never end up in `.tf` files. The URL **must not** contain them. The URL accepts [go-redis options](https://pkg.go.dev/github.com/redis/go-redis/v9#ParseURL), e.g.
`?dial_timeout=5s`.

## Development

Tools are managed with [mise](https://mise.jdx.dev) (`mise install`), tasks run with [moon](https://moonrepo.dev):

```sh
moon run build              # build the provider into ./bin
moon run :format            # format Go and Terraform files
moon run lint               # golangci-lint, vet, check go.mod is tidy
moon run docs               # generate docs/ (registry documentation) from the schema and examples/
moon run test-unit
moon run test-integration   # needs Docker, REDIS_IMAGE picks the server, e.g. valkey/valkey:9.1.3-alpine3.24
moon run examples/basic:execute   # plan, apply, plan, destroy the example
```

Integration tests and example tasks use OpenTofu (`tofu`), set `TERRAFORM_BINARY=terraform` to use Terraform instead.

## License

[MIT](LICENSE)
