package service

import (
	"testing"
	"time"
)

func TestRelativeTo(t *testing.T) {
	tests := []struct {
		name string
		root string
		full string
		want string
	}{
		{name: "root itself is empty", root: "/opt/app", full: "/opt/app", want: ""},
		{name: "direct child", root: "/opt/app", full: "/opt/app/config.yaml", want: "config.yaml"},
		{name: "nested child", root: "/opt/app", full: "/opt/app/etc/sub/x.conf", want: "etc/sub/x.conf"},
		{name: "trailing slash on root is normalized", root: "/opt/app/", full: "/opt/app/x", want: "x"},
		{name: "double slashes normalized", root: "/opt/app", full: "/opt/app//a//b", want: "a/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := relativeTo(tt.root, tt.full); got != tt.want {
				t.Fatalf("relativeTo(%q, %q) = %q, want %q", tt.root, tt.full, got, tt.want)
			}
		})
	}
}

func TestClampCopyTimeout(t *testing.T) {
	if got := clampCopyTimeout(0); got != copyDefaultTimeout {
		t.Fatalf("zero = %v, want default %v", got, copyDefaultTimeout)
	}
	if got := clampCopyTimeout(copyMaxTimeout + time.Hour); got != copyMaxTimeout {
		t.Fatalf("over-max = %v, want ceiling %v", got, copyMaxTimeout)
	}
	if got := clampCopyTimeout(time.Millisecond); got != time.Second {
		t.Fatalf("sub-second = %v, want 1s floor", got)
	}
	if got := clampCopyTimeout(2 * time.Minute); got != 2*time.Minute {
		t.Fatalf("in-range = %v, want unchanged", got)
	}
}
