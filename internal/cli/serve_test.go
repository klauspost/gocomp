package cli

import (
	"net"
	"testing"
)

type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

// A wildcard bind has no host a browser can open, and "-addr :0" only knows its
// port after Listen, which is why this reads the listener and not the flag.
func TestBrowseURL(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"127.0.0.1:8080", "http://127.0.0.1:8080/"},
		{"localhost:8080", "http://localhost:8080/"},
		{"0.0.0.0:8080", "http://localhost:8080/"},
		{"[::]:8080", "http://localhost:8080/"},
		{"[::1]:8080", "http://[::1]:8080/"},
	} {
		if got := browseURL(fakeAddr(tc.in)); got != tc.want {
			t.Errorf("browseURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBrowseURLUsesTheChosenPort(t *testing.T) {
	ln, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if got, want := browseURL(ln.Addr()), "http://127.0.0.1:"+port+"/"; got != want {
		// localhost may resolve to either loopback; accept the v6 form too.
		if got != "http://[::1]:"+port+"/" {
			t.Errorf("browseURL = %q, want %q", got, want)
		}
	}
}
