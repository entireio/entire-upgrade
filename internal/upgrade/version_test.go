package upgrade

import "testing"

func TestParseVersionFromEntireOutput(t *testing.T) {
	out := `Entire CLI 0.6.2-nightly.202605160654.ddf1a331 (ddf1a331)
Go version: go1.26.2
OS/Arch: darwin/amd64`

	got, err := ParseVersion(out)
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "0.6.2-nightly.202605160654.ddf1a331" {
		t.Fatalf("version = %q", got)
	}
	if !got.IsNightly() {
		t.Fatal("expected nightly version")
	}
}

func TestVersionCompare(t *testing.T) {
	tests := []struct {
		name string
		a    string
		b    string
		want int
	}{
		{
			name: "higher patch beats stable",
			a:    "0.6.2-nightly.202605160654.ddf1a331",
			b:    "0.6.1",
			want: 1,
		},
		{
			name: "stable beats prerelease with same base",
			a:    "0.6.2",
			b:    "0.6.2-nightly.202605160654.ddf1a331",
			want: 1,
		},
		{
			name: "newer nightly timestamp wins",
			a:    "0.6.2-nightly.202605160654.ddf1a331",
			b:    "0.6.2-nightly.202605150717.11da3db0",
			want: 1,
		},
		{
			name: "stable lower base loses",
			a:    "0.6.1",
			b:    "0.6.2-nightly.202605160654.ddf1a331",
			want: -1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := mustVersion(t, tt.a)
			b := mustVersion(t, tt.b)
			got := a.Compare(b)
			if got != tt.want {
				t.Fatalf("%s compare %s = %d, want %d", a, b, got, tt.want)
			}
		})
	}
}

func mustVersion(t *testing.T, s string) Version {
	t.Helper()
	v, err := ParseVersion(s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}
