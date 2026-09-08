module github.com/antifailure/antifailure/tools

go 1.26.0

toolchain go1.26.8

require (
	github.com/antifailure/antifailure/engine v0.0.0-20260827003151-4d231565e530
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/aws/aws-sdk-go-v2 v1.46.0 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.7.20 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.33.3 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.20.3 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.19.2 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.5.2 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.8.2 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.5.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/dynamodb v1.67.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/eventbridge v1.53.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.13.19 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.11.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/endpoint-discovery v1.13.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.14.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.20.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/kinesis v1.53.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/lambda v1.107.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/s3 v1.111.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/secretsmanager v1.48.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/signin v1.9.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sns v1.46.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sqs v1.51.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssm v1.77.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.37.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.42.0 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.49.0 // indirect
	github.com/aws/smithy-go v1.28.1 // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	golang.org/x/mod v0.39.0 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/telemetry v0.0.0-20260811182544-a038080d80e5 // indirect
	golang.org/x/text v0.41.0 // indirect
	golang.org/x/tools v0.49.0 // indirect
	golang.org/x/vuln v1.7.0 // indirect
)

tool golang.org/x/vuln/cmd/govulncheck

// The tools module is never published and never imported by anything, so it
// pins the engine to the working tree. Without this, GOWORK=off resolves the
// require below to whatever engine snapshot the module proxy last saw, and
// scanrepo, the gate that keeps live credentials out of the repository, would
// be checking a copy of the engine that is not the one being committed.
replace github.com/antifailure/antifailure/engine => ../engine
