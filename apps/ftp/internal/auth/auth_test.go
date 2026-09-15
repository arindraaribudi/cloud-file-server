package auth

import (
	"testing"
	"time"
)

func TestHashAndVerify(t *testing.T) {
	hash, err := HashPassword("S3cret!Pass")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(hash, "S3cret!Pass") {
		t.Error("verify should succeed")
	}
	if VerifyPassword(hash, "wrong") {
		t.Error("verify should fail")
	}
}

func TestLockout(t *testing.T) {
	l := NewLockout(3, 100*time.Millisecond)
	if !l.Allow("alice") {
		t.Fatal("first attempt allowed")
	}
	l.RecordFailure("alice")
	l.RecordFailure("alice")
	if !l.Allow("alice") {
		t.Fatal("should be allowed after 2 failures (limit is 3)")
	}
	l.RecordFailure("alice")
	if l.Allow("alice") {
		t.Fatal("locked after N failures")
	}
	time.Sleep(150 * time.Millisecond)
	if !l.Allow("alice") {
		t.Fatal("should unlock after window")
	}
}

func TestLockoutReset(t *testing.T) {
	l := NewLockout(2, time.Minute)
	l.RecordFailure("alice")
	l.RecordFailure("alice")
	if l.Allow("alice") {
		t.Fatal("locked before reset")
	}
	l.Reset("alice")
	if !l.Allow("alice") {
		t.Fatal("should be allowed after reset")
	}
}

func TestValidatePassword(t *testing.T) {
	cases := []struct {
		name    string
		pw      string
		wantErr bool
	}{
		{"valid", "Abcdefg1", false},
		{"too short", "Abc123", true},
		{"no upper", "abcdefg1", true},
		{"no lower", "ABCDEFG1", true},
		{"no digit", "Abcdefgh", true},
		{"empty", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidatePassword(c.pw)
			if (err != nil) != c.wantErr {
				t.Errorf("ValidatePassword(%q) err = %v, wantErr %v", c.pw, err, c.wantErr)
			}
		})
	}
}
