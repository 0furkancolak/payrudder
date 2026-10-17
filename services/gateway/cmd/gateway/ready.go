package main

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// grpcCodeIsUp reports whether an error still proves plans-svc answered (a NotFound for the probe prefix is healthy).
func grpcCodeIsUp(err error) bool {
	switch status.Code(err) {
	case codes.NotFound, codes.InvalidArgument:
		return true
	default:
		return false
	}
}
