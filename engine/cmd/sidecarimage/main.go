// Command sidecarimage prints the egress sidecar image's identity and writes
// its build context.
//
// WHY IT EXISTS. The release workflow has to publish the image the engine will
// ask for, and the engine asks for a content addressed reference: a SHA256 over
// the twenty source files tools/proxysrc packages plus the Dockerfile. So the
// workflow cannot name the tag and cannot assemble the context. It has to ask
// the engine, and this is the asking.
//
// THE ALTERNATIVE WAS A SECOND COPY OF THE HASH. A shell step in the workflow
// could hash the same files and usually agree, and the day it stopped agreeing
// the release would publish an image under a name no engine ever looks for, and
// every first run would fall back to a twenty five minute build with nothing
// saying why. One implementation, asked by both, is the only shape where that
// cannot happen.
//
// It writes the identical tar archive the engine hands its own daemon, so the
// published image is built from the bytes the engine would have built from
// rather than from a checkout that merely looks the same.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/antifailure/antifailure/engine/internal/proxyimage"
)

func main() {
	context := flag.String("context", "",
		"write the sidecar's build context tar to this path")
	flag.Parse()

	if *context != "" {
		if err := writeContext(*context); err != nil {
			fmt.Fprintln(os.Stderr, "sidecarimage:", err)
			os.Exit(1)
		}
	}

	// key=value lines, which is what GITHUB_OUTPUT takes. Printed even when a
	// context was written, because the step that builds needs the reference in
	// the same breath as the archive.
	fmt.Printf("digest=%s\n", proxyimage.SourcesDigest())
	fmt.Printf("local=%s\n", proxyimage.Tag())
	fmt.Printf("published=%s\n", proxyimage.PublishedRef())
	fmt.Printf("label=%s\n", proxyimage.SourcesLabel)
}

func writeContext(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, proxyimage.BuildContext()); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
