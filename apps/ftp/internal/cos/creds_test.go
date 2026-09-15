package cos

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestChainPrefersSTS(t *testing.T) {
	expiry := time.Now().Add(time.Hour)
	sts := &fakeSTS{out: creds{ID: "STS_ID", Key: "STS_KEY", Expiry: expiry}}
	st := &fakeStatic{id: "ST_ID", key: "ST_KEY"}
	c := newChain(sts, st, true, 0.8)
	got, src, err := c.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if src != "STS" || got.ID != "STS_ID" {
		t.Errorf("got=%+v src=%s", got, src)
	}
}

func TestChainFallsBackToStatic(t *testing.T) {
	sts := &fakeSTS{err: errors.New("sts down")}
	st := &fakeStatic{id: "ST_ID", key: "ST_KEY"}
	c := newChain(sts, st, true, 0.8)
	got, src, err := c.Get(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if src != "AKSK" || got.ID != "ST_ID" {
		t.Errorf("got=%+v src=%s", got, src)
	}
}

func TestChainFailsWhenNoFallback(t *testing.T) {
	sts := &fakeSTS{err: errors.New("sts down")}
	st := &fakeStatic{} // empty
	c := newChain(sts, st, true, 0.8)
	if _, _, err := c.Get(context.Background()); err == nil {
		t.Fatal("expected hard fail")
	}
}

type fakeSTS struct {
	out creds
	err error
}

func (f *fakeSTS) Get(ctx context.Context) (creds, error) { return f.out, f.err }

type fakeStatic struct{ id, key string }

func (f *fakeStatic) Get(ctx context.Context) (creds, error) {
	if f.id == "" {
		return creds{}, errors.New("no static creds")
	}
	return creds{ID: f.id, Key: f.key, Expiry: time.Now().Add(time.Hour)}, nil
}
