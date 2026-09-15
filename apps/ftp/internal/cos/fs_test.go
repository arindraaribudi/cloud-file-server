package cos

import (
	"testing"
	"time"
)

func TestNewClient(t *testing.T) {
	bucket := "bucket-1"
	region := "ap-guangzhou"
	c := creds{ID: "id", Key: "key", Expiry: time.Now().Add(time.Hour)}
	client := NewClient(bucket, region, c, nil)
	if client == nil {
		t.Fatal("got nil client")
	}
	if client.Bucket != bucket || client.Region != region {
		t.Errorf("got bucket=%q region=%q", client.Bucket, client.Region)
	}
}