package termimg

import (
	"os"
	"strings"
	"testing"
)

// On Windows no question is written, because its answer could not be read
// back within a deadline and would be left for the dashboard to read as typed
// keys. Detect then says why and names the variable that decides instead,
// rather than reporting a terminal that draws nothing.
func TestWindowsAsksNothingAndSaysWhy(t *testing.T) {
	in, inw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close(); _ = inw.Close() }()
	outr, out, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	got := Detect(in, out, envOf(nil))
	_ = out.Close()
	written, err := readAll(outr)
	if err != nil {
		t.Fatal(err)
	}
	if written != "" {
		t.Fatalf("a question was written to the terminal: %q", written)
	}
	if !strings.Contains(got.Why, "AF_IMAGES") {
		t.Fatalf("the reason does not say how to choose: %q", got.Why)
	}
	if got.Protocol != NoImages {
		t.Fatalf("a terminal nobody asked was given a protocol: %v", got.Protocol)
	}
}

func readAll(f *os.File) (string, error) {
	defer func() { _ = f.Close() }()
	var b strings.Builder
	buf := make([]byte, 512)
	for {
		n, err := f.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return b.String(), nil
		}
	}
}
