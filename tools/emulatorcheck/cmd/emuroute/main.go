// Command emuroute is the sidecar's routing, on its own, inside a container.
//
// It exists because the AWS SDK for JavaScript reads no proxy variable. The
// environment's real mechanism is not the proxy variables, which any library
// is free to ignore: it is DNS. Every external name resolves to the sidecar,
// which terminates TLS with an authority the environment already trusts, and a
// client that ignores every variable still arrives there. A Docker network
// alias per hostname stands in for the environment's resolver, and this is
// what the aliases point at.
//
// It routes a covered host to the emulator with its Host and Authorization
// headers untouched, refuses everything else with the sidecar's block response, and
// writes one line per decision to standard output so that the suite can assert
// on what the SDK actually sent.
package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/antifailure/antifailure/tools/emulatorcheck"
)

func main() {
	listen := flag.String("listen", ":443", "where to answer HTTPS")
	plainListen := flag.String("http-listen", ":80", "where to answer HTTP")
	target := flag.String("emulator", "", "the emulator, as host:port")
	caCert := flag.String("ca-cert", "", "the environment's certificate authority, in PEM")
	caKey := flag.String("ca-key", "", "its private key, in PEM")
	flag.Parse()

	if *target == "" || *caCert == "" || *caKey == "" {
		log.Fatal("emuroute: -emulator, -ca-cert and -ca-key are all required")
	}
	cert, err := os.ReadFile(*caCert)
	if err != nil {
		log.Fatalf("emuroute: %v", err)
	}
	key, err := os.ReadFile(*caKey)
	if err != nil {
		log.Fatalf("emuroute: %v", err)
	}

	sidecar, err := emulatorcheck.LoadSidecar(*target, cert, key)
	if err != nil {
		log.Fatalf("emuroute: %v", err)
	}
	sidecar.Log = os.Stdout
	plain, err := net.Listen("tcp", *plainListen)
	if err != nil {
		log.Fatalf("emuroute: %v", err)
	}
	go func() {
		server := &http.Server{Handler: sidecar, ReadHeaderTimeout: 30 * time.Second}
		if err := server.Serve(plain); err != nil {
			log.Fatalf("emuroute HTTP: %v", err)
		}
	}()

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatalf("emuroute: %v", err)
	}
	log.Printf("emuroute: answering on %s for %s", *listen, *target)
	if err := sidecar.ServeTLS(ln); err != nil {
		log.Fatalf("emuroute: %v", err)
	}
}
