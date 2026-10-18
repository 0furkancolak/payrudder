module github.com/0furkancolak/quotify/tools/quotifyctl

go 1.27.2

require (
	github.com/0furkancolak/quotify/pkg v0.0.0
	github.com/rabbitmq/amqp091-go v1.15.0
)

replace github.com/0furkancolak/quotify/pkg => ../../pkg

replace github.com/0furkancolak/quotify/gen/go => ../../gen/go
