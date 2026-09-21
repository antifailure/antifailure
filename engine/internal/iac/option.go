package iac

// Option configures a read.
//
// Options are deliberately few and every one of them narrows what the reader
// has to guess. None of them can widen what it is allowed to do: there is no
// option that lets a caller execute anything, reach a network, or read a state
// file, because those refusals are properties of the package rather than
// defaults a caller can turn off. An option that could switch a safety control
// off is a safety control that is off.
type Option func(*config)

type config struct {
	workspace string
	varFiles  []string
	maxBytes  int64
}

// defaultMaxBytes is the largest file this reader will read into memory.
//
// Hand written infrastructure as code is kilobytes. A file above this is a
// generated artefact, a vendored tree or a state file somebody renamed, and
// reading it would be a memory cost paid for nothing. Over the limit the file
// becomes a Source with Read false and the reason, which is the honest answer
// rather than a silent skip.
const defaultMaxBytes = 32 << 20

func newConfig(opts ...Option) *config {
	cfg := &config{maxBytes: defaultMaxBytes}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	return cfg
}

// WithWorkspace binds `terraform.workspace` to a name.
//
// It changes answers rather than decorating them. Configurations interpolate
// the workspace into resource names constantly, `"${terraform.workspace}-assets"`,
// and without the name every one of those is Unreadable. With it they resolve.
// Unset, `terraform.workspace` stays Unreadable and says so, which is correct:
// this reader does not know which workspace a plan would run in, and "default"
// is a guess that would be wrong in exactly the environments that matter.
func WithWorkspace(name string) Option {
	return func(c *config) { c.workspace = name }
}

// WithVarFiles names tfvars files whose values take part in resolution.
//
// Paths are relative to the root being read. A variable with no default is
// Unreadable; the same variable set in one of these files is Known. Later
// files win over earlier ones, which is Terraform's own rule for repeated
// -var-file flags.
func WithVarFiles(paths ...string) Option {
	return func(c *config) { c.varFiles = append(c.varFiles, paths...) }
}

// withMaxBytes lowers the file size ceiling. Unexported: it exists so a test
// can reach the limit without writing a 32 MiB fixture, and a caller has no
// reason to want it.
func withMaxBytes(n int64) Option {
	return func(c *config) { c.maxBytes = n }
}
