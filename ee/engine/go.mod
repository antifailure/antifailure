// The enterprise engine is a separate Go module, deliberately.
//
// Not a build tag and not a subdirectory of the community module. A separate
// module means the community binary cannot import this code even by accident:
// there is no import path that resolves, so a mistaken import is a compile
// error on the machine that made it rather than a boundary violation that a
// linter has to notice later.
//
// It is also kept out of the root go.work for the same reason. The edition
// boundary job in CI deletes this directory outright and builds and tests the
// community engine, which has to keep working, and a workspace entry pointing
// at a deleted directory would break that check by breaking everything.
module github.com/antifailure/antifailure/ee/engine

go 1.25.0

// The community engine is consumed from the same checkout rather than from a
// published version, because the two are released from one tag.
replace github.com/antifailure/antifailure/engine => ../../engine

require github.com/stretchr/testify v1.12.1

require go.yaml.in/yaml/v3 v3.0.5 // indirect
