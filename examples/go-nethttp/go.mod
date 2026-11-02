module payrudder-example-nethttp

go 1.23

require github.com/0furkancolak/payrudder/sdk/go v0.0.0

// Not on a module proxy yet: the SDK is the one of this checkout.
replace github.com/0furkancolak/payrudder/sdk/go => ../../sdk/go
