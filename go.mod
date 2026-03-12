module github.com/nomand-zc/lumin-proxy

go 1.24.11

require (
	github.com/go-kratos/kratos/v2 v2.9.2
	github.com/nomand-zc/lumin-acpool v0.0.0
	github.com/nomand-zc/lumin-client v0.0.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/go-kratos/aegis v0.2.0 // indirect
	github.com/go-playground/form/v4 v4.2.0 // indirect
	github.com/golang/protobuf v1.5.4 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.1 // indirect
	golang.org/x/sync v0.11.0 // indirect
	golang.org/x/sys v0.28.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20240102182953-50ed04b92917 // indirect
	google.golang.org/grpc v1.61.1 // indirect
	google.golang.org/protobuf v1.33.0 // indirect
)

replace (
	github.com/nomand-zc/lumin-acpool => ../lumin-acpool
	github.com/nomand-zc/lumin-client => ../lumin-client
)
