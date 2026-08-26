// Command licensegen issues and inspects enterprise license keys.
//
// It lives in tools/ rather than in the shipped engine because issuing is
// something the vendor does and verifying is something the software does, and
// those want different code with different keys. The engine carries public keys
// and can only check; this carries a private key and can only be run by
// somebody who has one.
//
// The private key is never written by this program and never read from a file
// in the repository. It arrives in an environment variable that a key vault
// puts there for the length of one command, which is the only handling that
// makes "the signing key never leaves the vault" true rather than aspirational.
package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "licensegen:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("a subcommand is required")
	}
	switch args[0] {
	case "keygen":
		return keygen(args[1:])
	case "issue":
		return issue(args[1:])
	case "inspect":
		return inspect(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, strings.TrimSpace(`
licensegen issues and inspects Antifailure enterprise licenses.

  keygen              Generate a signing key pair. The private half is printed
                      once, to standard output, and never written to a file.
  issue               Sign a license. Reads the private key from
                      AF_LICENSE_SIGNING_KEY, which a key vault sets for the
                      length of one command.
  inspect             Decode a license without verifying it, for support.

`)+"\n")
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	id := fs.String("id", "", "identifier for this key, such as 2026-01")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *id == "" {
		return errors.New("-id is required, so that a rotation can be traced")
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}

	// Printed rather than written. A private key in a file is a private key that
	// gets committed, backed up, and copied to a laptop. The operator is
	// expected to paste this straight into a key vault, and the instruction says
	// so rather than assuming they know.
	fmt.Printf("key id:      %s\n", *id)
	fmt.Printf("public key:  %s\n", base64.RawURLEncoding.EncodeToString(pub))
	fmt.Printf("private key: %s\n", base64.RawURLEncoding.EncodeToString(priv))
	fmt.Print("\n" + strings.TrimSpace(`
Put the private key in the key vault now and do not save it anywhere else. Add
the public key to the verifier's embedded keys, keeping every previous key: a
build that trusts only the newest key cannot verify any license already issued.
`) + "\n")
	return nil
}

// request is the YAML-shaped input for issuing, accepted as JSON so that this
// tool needs no dependencies.
type request struct {
	Org       string   `json:"org"`
	Plan      string   `json:"plan"`
	Features  []string `json:"features"`
	Seats     int      `json:"seats"`
	Months    int      `json:"months"`
	GraceDays int      `json:"grace_days"`
	Trial     bool     `json:"trial"`
}

func issue(args []string) error {
	fs := flag.NewFlagSet("issue", flag.ContinueOnError)
	file := fs.String("request", "", "path to the issuance request, or - for standard input")
	keyID := fs.String("key-id", "", "which signing key this is")
	id := fs.String("id", "", "license identifier, for the revocation list")
	now := fs.String("now", "", "issue time in RFC 3339, for reproducible output in tests")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *file == "" || *keyID == "" || *id == "" {
		return errors.New("-request, -key-id, and -id are all required")
	}

	secret := os.Getenv("AF_LICENSE_SIGNING_KEY")
	if secret == "" {
		return errors.New(
			"AF_LICENSE_SIGNING_KEY is not set. It should be supplied by the key vault for " +
				"the length of this command and never stored on disk")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(secret))
	if err != nil {
		return fmt.Errorf("the signing key is not base64url: %w", err)
	}
	if len(raw) != ed25519.PrivateKeySize {
		return fmt.Errorf("the signing key is %d bytes and should be %d", len(raw), ed25519.PrivateKeySize)
	}
	priv := ed25519.PrivateKey(raw)

	var body []byte
	if *file == "-" {
		body, err = os.ReadFile("/dev/stdin")
	} else {
		body, err = os.ReadFile(*file)
	}
	if err != nil {
		return err
	}
	var req request
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("the request is not valid JSON: %w", err)
	}
	if req.Org == "" {
		return errors.New("the request names no organization, and a license that names none works everywhere")
	}
	if req.Months <= 0 {
		req.Months = 12
	}

	issuedAt := time.Now().UTC()
	if *now != "" {
		issuedAt, err = time.Parse(time.RFC3339, *now)
		if err != nil {
			return err
		}
	}

	claims := map[string]any{
		"id":         *id,
		"org":        req.Org,
		"plan":       req.Plan,
		"features":   req.Features,
		"seats":      req.Seats,
		"issued_at":  issuedAt.Format(time.RFC3339Nano),
		"expires_at": issuedAt.AddDate(0, req.Months, 0).Format(time.RFC3339Nano),
		"kid":        *keyID,
	}
	if req.GraceDays > 0 {
		claims["grace_days"] = req.GraceDays
	}
	if req.Trial {
		claims["trial"] = true
	}

	payload, err := json.Marshal(claims)
	if err != nil {
		return err
	}
	signature := ed25519.Sign(priv, payload)

	fmt.Printf("aflic_%s.%s\n",
		base64.RawURLEncoding.EncodeToString(payload),
		base64.RawURLEncoding.EncodeToString(signature))
	return nil
}

func inspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	token := fs.String("token", "", "the license key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *token == "" {
		return errors.New("-token is required")
	}

	body := strings.TrimPrefix(strings.Join(strings.Fields(*token), ""), "aflic_")
	dot := strings.IndexByte(body, '.')
	if dot <= 0 {
		return errors.New("that is not a license key")
	}
	payload, err := base64.RawURLEncoding.DecodeString(body[:dot])
	if err != nil {
		return fmt.Errorf("the payload is not base64url: %w", err)
	}

	var pretty map[string]any
	if err := json.Unmarshal(payload, &pretty); err != nil {
		return fmt.Errorf("the payload is not JSON: %w", err)
	}
	out, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		return err
	}
	// Stated plainly, because this is the command a support engineer runs while
	// a customer is on the phone and the difference matters.
	fmt.Println(string(out))
	fmt.Fprintln(os.Stderr,
		"\nThis decoded the key without checking its signature. Anybody can write these; "+
			"only the engine's verification says whether one is real.")
	return nil
}
