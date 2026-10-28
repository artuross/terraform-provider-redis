#!lua name=hello

redis.register_function('hello', function(keys, args)
	return 'Hello, World!'
end)
