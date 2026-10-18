//go:build !chaos

package main

func afterCommitFault() func() error { return nil }
