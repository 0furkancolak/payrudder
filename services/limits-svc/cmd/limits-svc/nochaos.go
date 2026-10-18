//go:build !chaos

package main

func afterLuaFault() func() error { return nil }
