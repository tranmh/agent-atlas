package httpx

import (
	"testing"
	"time"
)

func TestGetEnv(t *testing.T) {
	t.Setenv("HX_STR", "v")
	t.Setenv("HX_EMPTY", "")
	if GetEnv("HX_STR", "d") != "v" || GetEnv("HX_EMPTY", "d") != "d" || GetEnv("HX_UNSET_XYZ", "d") != "d" {
		t.Fatal("GetEnv")
	}
}

func TestGetEnvInt(t *testing.T) {
	t.Setenv("HX_INT", "42")
	if n, err := GetEnvInt("HX_INT", 1); err != nil || n != 42 {
		t.Fatalf("got %d %v", n, err)
	}
	if n, err := GetEnvInt("HX_UNSET_XYZ", 7); err != nil || n != 7 {
		t.Fatalf("default %d %v", n, err)
	}
	t.Setenv("HX_INT", "x")
	if _, err := GetEnvInt("HX_INT", 1); err == nil {
		t.Fatal("want error")
	}
}

func TestGetEnvBool(t *testing.T) {
	for v, want := range map[string]bool{"1": true, "true": true, "YES": true, "on": true, "0": false, "false": false, "nope": false} {
		t.Setenv("HX_BOOL", v)
		if got := GetEnvBool("HX_BOOL", !want); got != want {
			t.Errorf("%q => %v", v, got)
		}
	}
	if !GetEnvBool("HX_UNSET_XYZ", true) {
		t.Error("default")
	}
}

func TestGetEnvDuration(t *testing.T) {
	t.Setenv("HX_DUR", "90s")
	if d, err := GetEnvDuration("HX_DUR", time.Second); err != nil || d != 90*time.Second {
		t.Fatalf("got %v %v", d, err)
	}
	if d, _ := GetEnvDuration("HX_UNSET_XYZ", 5*time.Second); d != 5*time.Second {
		t.Fatal("default")
	}
	t.Setenv("HX_DUR", "5")
	if _, err := GetEnvDuration("HX_DUR", time.Second); err == nil {
		t.Fatal("want error")
	}
}

func TestRequireEnv(t *testing.T) {
	t.Setenv("HX_REQ", "x")
	if v, err := RequireEnv("HX_REQ"); err != nil || v != "x" {
		t.Fatal(v, err)
	}
	t.Setenv("HX_REQ", "")
	if _, err := RequireEnv("HX_REQ"); err == nil {
		t.Fatal("want error for empty")
	}
}
