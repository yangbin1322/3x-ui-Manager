package service

import (
	"testing"
	"time"
)

func TestResolveRemotePath(t *testing.T) {
	tests := []struct {
		name     string
		dest     string
		fileName string
		want     string
		wantErr  bool
	}{
		{name: "full file path is used verbatim", dest: "/etc/app/config.yaml", fileName: "local.yaml", want: "/etc/app/config.yaml"},
		{name: "directory dest keeps original name", dest: "/root/", fileName: "app.conf", want: "/root/app.conf"},
		{name: "directory dest base-cleans a traversal filename", dest: "/root/", fileName: "../../etc/passwd", want: "/root/passwd"},
		{name: "directory dest strips windows path in filename", dest: "/root/", fileName: `C:\evil\payload.sh`, want: "/root/payload.sh"},
		{name: "nested directory dest", dest: "/opt/data/", fileName: "x.bin", want: "/opt/data/x.bin"},
		{name: "empty dest is rejected", dest: "", fileName: "x", wantErr: true},
		{name: "whitespace dest is rejected", dest: "   ", fileName: "x", wantErr: true},
		{name: "directory dest with empty filename is rejected", dest: "/root/", fileName: "", wantErr: true},
		{name: "directory dest with dot filename is rejected", dest: "/root/", fileName: ".", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveRemotePath(tt.dest, tt.fileName)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("resolveRemotePath(%q, %q) = %q, want error", tt.dest, tt.fileName, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRemotePath(%q, %q) unexpected error: %v", tt.dest, tt.fileName, err)
			}
			if got != tt.want {
				t.Fatalf("resolveRemotePath(%q, %q) = %q, want %q", tt.dest, tt.fileName, got, tt.want)
			}
		})
	}
}

func TestSafeRel(t *testing.T) {
	tests := []struct {
		name string
		rel  string
		fn   string
		want string
	}{
		{name: "simple relative path kept", rel: "mydir/a.txt", fn: "a.txt", want: "mydir/a.txt"},
		{name: "nested relative path kept", rel: "d/sub/x.conf", fn: "x.conf", want: "d/sub/x.conf"},
		{name: "leading slash stripped", rel: "/etc/passwd", fn: "passwd", want: "etc/passwd"},
		{name: "traversal segments dropped", rel: "../../etc/passwd", fn: "passwd", want: "etc/passwd"},
		{name: "backslashes normalized", rel: `dir\sub\a.txt`, fn: "a.txt", want: "dir/sub/a.txt"},
		{name: "empty rel falls back to filename", rel: "", fn: "a.txt", want: "a.txt"},
		{name: "empty rel base-cleans filename", rel: "", fn: "../../a.txt", want: "a.txt"},
		{name: "dot rel falls back to filename", rel: ".", fn: "a.txt", want: "a.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := safeRel(tt.rel, tt.fn); got != tt.want {
				t.Fatalf("safeRel(%q, %q) = %q, want %q", tt.rel, tt.fn, got, tt.want)
			}
		})
	}
}

func TestTreeUpload(t *testing.T) {
	if treeUpload([]UploadEntry{{Name: "a"}}) {
		t.Fatal("single file with no rel should not be a tree upload")
	}
	if !treeUpload([]UploadEntry{{Name: "a"}, {Name: "b"}}) {
		t.Fatal("multiple files should be a tree upload")
	}
	if !treeUpload([]UploadEntry{{Name: "a", Rel: "d/a"}}) {
		t.Fatal("single file with a rel should be a tree upload")
	}
}

func TestClampUploadTimeout(t *testing.T) {
	if got := clampUploadTimeout(0); got != uploadDefaultTimeout {
		t.Fatalf("zero timeout = %v, want default %v", got, uploadDefaultTimeout)
	}
	if got := clampUploadTimeout(uploadMaxTimeout + time.Hour); got != uploadMaxTimeout {
		t.Fatalf("over-max timeout = %v, want ceiling %v", got, uploadMaxTimeout)
	}
	if got := clampUploadTimeout(time.Millisecond); got != time.Second {
		t.Fatalf("sub-second timeout = %v, want 1s floor", got)
	}
	if got := clampUploadTimeout(90 * time.Second); got != 90*time.Second {
		t.Fatalf("in-range timeout = %v, want unchanged", got)
	}
}
