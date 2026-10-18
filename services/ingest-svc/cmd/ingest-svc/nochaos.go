//go:build !chaos

package main

func afterPublishFault() func() error { return nil }
