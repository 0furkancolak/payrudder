package main

import "testing"

// Object storage is all or nothing: without any setting the PDF links are off, with an endpoint or one key and not the rest the service refuses to start.
func TestObjectStorageIsAllOrNothing(t *testing.T) {
	for name, c := range map[string]struct {
		cfg  Config
		fail bool
	}{
		"nothing":           {Config{}, false},
		"endpoint and keys": {Config{S3Endpoint: "http://s3", S3AccessKey: "a", S3SecretKey: "s"}, false},
		"keys, no endpoint": {Config{S3AccessKey: "a", S3SecretKey: "s"}, false},
		"endpoint, no keys": {Config{S3Endpoint: "http://s3"}, true},
		"access key alone":  {Config{S3AccessKey: "a"}, true},
		"secret key alone":  {Config{S3SecretKey: "s"}, true},
		"endpoint, one key": {Config{S3Endpoint: "http://s3", S3AccessKey: "a"}, true},
		"bucket and region": {Config{S3Bucket: "invoices", S3Region: "us-east-1"}, false},
	} {
		if err := c.cfg.checkObjectStorage(); (err != nil) != c.fail {
			t.Errorf("%s: %v, want failure %v", name, err, c.fail)
		}
	}
}
